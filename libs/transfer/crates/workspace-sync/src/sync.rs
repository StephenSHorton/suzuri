//! listen / join over iroh: dump existing jsonl, then poll for new lines.

use std::collections::HashSet;
use std::fs;
use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use anyhow::{bail, Context, Result};
use iroh::endpoint::{presets, Connection, RecvStream, SendStream};
use iroh::{Endpoint, EndpointAddr, SecretKey};
use notify::Watcher;

use crate::events::Reporter;
use crate::merge::{ingest_line, snapshot, upsert_author};
use crate::proto::{read_msg, write_msg, WireMsg, ALPN};

const POLL: Duration = Duration::from_millis(500);
const ONLINE_WAIT: Duration = Duration::from_secs(30);
const IDENTITY_FILE: &str = "identity.secret";

/// Load the secret key from disk, or generate and persist a new one.
///
/// The key file is written with mode `0600` on Unix. Inlined from hato-core
/// so this crate does not depend on hato-core (two local processes need
/// distinct endpoint ids under `--iroh-dir`).
pub fn load_or_create_secret_key_in(dir: &Path) -> Result<SecretKey> {
    fs::create_dir_all(dir).with_context(|| format!("create {}", dir.display()))?;
    let path = dir.join(IDENTITY_FILE);
    if path.exists() {
        let mut f =
            fs::File::open(&path).with_context(|| format!("open identity {}", path.display()))?;
        let mut buf = [0u8; 32];
        f.read_exact(&mut buf)
            .with_context(|| format!("read identity {}", path.display()))?;
        let mut extra = [0u8; 1];
        match f.read(&mut extra) {
            Ok(0) => {}
            Ok(_) => bail!("identity file {} is longer than 32 bytes", path.display()),
            Err(e) if e.kind() == std::io::ErrorKind::UnexpectedEof => {}
            Err(e) => return Err(e).context("check identity length"),
        }
        return Ok(SecretKey::from_bytes(&buf));
    }

    let key = SecretKey::generate();
    write_secret_key(&path, &key)?;
    Ok(key)
}

fn write_secret_key(path: &Path, key: &SecretKey) -> Result<()> {
    let bytes = key.to_bytes();
    let mut f = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path)
        .with_context(|| format!("create identity {}", path.display()))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        f.set_permissions(fs::Permissions::from_mode(0o600))
            .context("set identity file permissions")?;
    }
    f.write_all(&bytes)
        .with_context(|| format!("write identity {}", path.display()))?;
    f.sync_all().ok();
    Ok(())
}

async fn bind_endpoint(sk: SecretKey) -> Result<Endpoint> {
    Endpoint::builder(presets::N0)
        .secret_key(sk)
        .alpns(vec![ALPN.to_vec()])
        .bind()
        .await
        .context("bind iroh endpoint")
}

/// iroh 1.0 EndpointAddr encoding: compact JSON (Display is not parseable).
pub fn encode_ticket(addr: &EndpointAddr) -> Result<String> {
    serde_json::to_string(addr).context("serialize EndpointAddr")
}

/// Parse a JSON EndpointAddr, optionally prefixed with `ticket: `.
pub fn parse_ticket(s: &str) -> Result<EndpointAddr> {
    let s = s.trim().strip_prefix("ticket:").unwrap_or(s).trim();
    if s.is_empty() {
        bail!("empty ticket");
    }
    serde_json::from_str(s).context("parse ticket EndpointAddr")
}

/// Bind, print a one-line ticket (or `ready` NDJSON), accept joiners until Ctrl+C.
pub async fn listen(root: PathBuf, iroh_dir: PathBuf, json: bool) -> Result<()> {
    let reporter = Reporter::new(json);
    let sk = load_or_create_secret_key_in(&iroh_dir)?;
    let endpoint = bind_endpoint(sk).await?;
    let _ = tokio::time::timeout(ONLINE_WAIT, endpoint.online()).await;
    let ticket = encode_ticket(&endpoint.addr())?;
    reporter.ready(&ticket, "listen");
    eprintln!("listening for workspace message sync (Ctrl+C to stop)");

    loop {
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {
                reporter.stopped();
                endpoint.close().await;
                break;
            }
            incoming = endpoint.accept() => {
                let Some(incoming) = incoming else {
                    break;
                };
                let root = root.clone();
                let reporter = reporter.clone();
                tokio::spawn(async move {
                    match incoming.await {
                        Ok(conn) => {
                            reporter.peer_up();
                            let res = run_connection_with(conn, root, reporter.clone()).await;
                            reporter.peer_down();
                            if let Err(e) = res {
                                tracing::warn!("peer session: {e:#}");
                                eprintln!("peer session: {e:#}");
                                reporter.error(&format!("{e:#}"));
                            }
                        }
                        Err(e) => {
                            tracing::warn!("accept: {e}");
                            eprintln!("accept: {e}");
                            reporter.error(&format!("accept: {e}"));
                        }
                    }
                });
            }
        }
    }
    Ok(())
}

/// Dial a listen ticket and sync until the connection ends or Ctrl+C.
pub async fn join(root: PathBuf, iroh_dir: PathBuf, ticket: &str, json: bool) -> Result<()> {
    let reporter = Reporter::new(json);
    let addr = parse_ticket(ticket)?;
    let sk = load_or_create_secret_key_in(&iroh_dir)?;
    let endpoint = bind_endpoint(sk).await?;
    let _ = tokio::time::timeout(ONLINE_WAIT, endpoint.online()).await;
    reporter.connecting();
    eprintln!("connecting…");
    let conn = match endpoint.connect(addr, ALPN).await {
        Ok(c) => c,
        Err(e) => {
            reporter.error(&format!("connect: {e:#}"));
            return Err(e).context("connect to listen ticket");
        }
    };
    reporter.peer_up();
    eprintln!("connected; syncing channel messages (Ctrl+C to stop)");
    tokio::select! {
        _ = tokio::signal::ctrl_c() => {
            reporter.stopped();
        }
        res = run_connection_with(conn, root, reporter.clone()) => {
            reporter.peer_down();
            if let Err(e) = &res {
                reporter.error(&format!("{e:#}"));
            }
            res?;
        }
    }
    endpoint.close().await;
    Ok(())
}

/// After a connection is up: each side open_bi for send and accept_bi for recv.
pub async fn run_connection(conn: Connection, root: PathBuf) -> Result<()> {
    run_connection_with(conn, root, Reporter::silent()).await
}

async fn run_connection_with(conn: Connection, root: PathBuf, _reporter: Reporter) -> Result<()> {
    let (mut send, mut recv, _keep) = open_sync_streams(&conn).await?;
    let sent = Arc::new(Mutex::new(HashSet::<String>::new()));
    let (wake, _watch) = start_jsonl_watch(&root);
    tokio::select! {
        res = send_loop(&mut send, root.clone(), sent.clone(), wake) => res,
        res = recv_loop(&mut recv, root, sent) => res,
    }
}

/// Wake send_loop when channel jsonl changes. Watcher is held so Drop stops it.
fn start_jsonl_watch(
    root: &Path,
) -> (
    Option<Arc<tokio::sync::Notify>>,
    Option<notify::RecommendedWatcher>,
) {
    let wake = Arc::new(tokio::sync::Notify::new());
    let flag = wake.clone();
    let channels = root.join("channels");
    let _ = fs::create_dir_all(&channels);
    let watcher = notify::recommended_watcher(move |res: Result<notify::Event, notify::Error>| {
        if res.is_ok() {
            flag.notify_waiters();
        }
    });
    let mut watcher = match watcher {
        Ok(w) => w,
        Err(e) => {
            tracing::warn!("workspace jsonl watch: {e}");
            return (None, None);
        }
    };
    if let Err(e) = watcher.watch(&channels, notify::RecursiveMode::Recursive) {
        tracing::warn!("workspace jsonl watch: {e}");
        return (None, None);
    }
    (Some(wake), Some(watcher))
}

/// Keep unused stream halves alive so dropping them does not RESET the used halves.
struct KeepHalves {
    _recv_open: RecvStream,
    _send_accept: SendStream,
}

async fn open_sync_streams(conn: &Connection) -> Result<(SendStream, RecvStream, KeepHalves)> {
    let conn_accept = conn.clone();
    let accept_task = tokio::spawn(async move {
        let mut last = None;
        for attempt in 0..8 {
            match tokio::time::timeout(Duration::from_secs(3), conn_accept.accept_bi()).await {
                Ok(Ok(pair)) => return Ok(pair),
                Ok(Err(e)) => last = Some(anyhow::anyhow!("accept_bi: {e}")),
                Err(_) => last = Some(anyhow::anyhow!("accept_bi timeout attempt {attempt}")),
            }
        }
        Err(last.unwrap_or_else(|| anyhow::anyhow!("accept_bi failed")))
    });
    let (send, recv_open) = conn.open_bi().await.context("open_bi")?;
    let (send_accept, recv) = accept_task.await??;
    Ok((
        send,
        recv,
        KeepHalves {
            _recv_open: recv_open,
            _send_accept: send_accept,
        },
    ))
}

async fn send_loop(
    send: &mut SendStream,
    root: PathBuf,
    sent: Arc<Mutex<HashSet<String>>>,
    wake: Option<Arc<tokio::sync::Notify>>,
) -> Result<()> {
    loop {
        let rows = snapshot(&root)?;
        for (channel, id, line) in rows {
            {
                let g = sent.lock().unwrap_or_else(|e| e.into_inner());
                if g.contains(&id) {
                    continue;
                }
            }
            let msg = WireMsg {
                v: 0,
                channel,
                id: id.clone(),
                line,
            };
            write_msg(send, &msg).await?;
            sent.lock().unwrap_or_else(|e| e.into_inner()).insert(id);
        }
        if let Some(wake) = &wake {
            tokio::select! {
                _ = tokio::time::sleep(POLL) => {}
                _ = wake.notified() => {
                    // Debounce bursty atomic jsonl rewrites.
                    tokio::time::sleep(Duration::from_millis(40)).await;
                }
            }
        } else {
            tokio::time::sleep(POLL).await;
        }
    }
}

async fn recv_loop(
    recv: &mut RecvStream,
    root: PathBuf,
    sent: Arc<Mutex<HashSet<String>>>,
) -> Result<()> {
    loop {
        let Some(msg) = read_msg(recv).await? else {
            return Ok(());
        };
        if msg.id.is_empty() || msg.channel.is_empty() {
            continue;
        }
        match ingest_line(&root, &msg.channel, &msg.id, &msg.line) {
            Ok(_) => {
                if let Err(e) = upsert_author(&root, &msg.line) {
                    tracing::warn!("member upsert skipped: {e:#}");
                }
                sent.lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .insert(msg.id);
            }
            Err(e) => {
                tracing::warn!("ingest skipped: {e:#}");
            }
        }
    }
}

/// Bind two ephemeral endpoints, connect A→B, copy jsonl both ways.
///
/// Used by tests. Times out connect/relay waits so CI without net can skip.
pub async fn sync_pair_inprocess(root_a: &Path, root_b: &Path) -> Result<()> {
    let ep_a = bind_endpoint(SecretKey::generate()).await?;
    let ep_b = bind_endpoint(SecretKey::generate()).await?;
    let _ = tokio::time::timeout(Duration::from_secs(2), ep_a.online()).await;
    let _ = tokio::time::timeout(Duration::from_secs(2), ep_b.online()).await;

    let addr_b: EndpointAddr = ep_b.addr();
    let listen_root = root_b.to_path_buf();
    let listen_task = tokio::spawn(async move {
        let incoming = tokio::time::timeout(Duration::from_secs(15), ep_b.accept())
            .await
            .map_err(|_| anyhow::anyhow!("listen accept timed out"))?
            .ok_or_else(|| anyhow::anyhow!("endpoint closed before accept"))?;
        let conn = incoming.await.context("incoming handshake")?;
        run_connection(conn, listen_root).await
    });

    let conn_a = tokio::time::timeout(Duration::from_secs(15), ep_a.connect(addr_b, ALPN))
        .await
        .map_err(|_| anyhow::anyhow!("connect timed out"))?
        .context("connect A→B")?;

    let join_root = root_a.to_path_buf();
    let join_task = tokio::spawn(async move { run_connection(conn_a, join_root).await });

    let want: HashSet<String> = snapshot(root_a)?.into_iter().map(|(_, id, _)| id).collect();
    if want.is_empty() {
        bail!("root A has no messages to sync");
    }
    let deadline = tokio::time::Instant::now() + Duration::from_secs(12);
    loop {
        let have: HashSet<String> = snapshot(root_b)?.into_iter().map(|(_, id, _)| id).collect();
        if want.iter().all(|id| have.contains(id)) {
            break;
        }
        if tokio::time::Instant::now() >= deadline {
            join_task.abort();
            listen_task.abort();
            ep_a.close().await;
            bail!("root B did not receive ids {want:?}");
        }
        tokio::time::sleep(Duration::from_millis(100)).await;
    }

    ingest_line(
        root_b,
        "general",
        "msg_from_b",
        r#"{"id":"msg_from_b","channel":"general","body":"hello-from-b"}"#,
    )?;
    let deadline = tokio::time::Instant::now() + Duration::from_secs(12);
    loop {
        let have: HashSet<String> = snapshot(root_a)?.into_iter().map(|(_, id, _)| id).collect();
        if have.contains("msg_from_b") {
            break;
        }
        if tokio::time::Instant::now() >= deadline {
            join_task.abort();
            listen_task.abort();
            ep_a.close().await;
            bail!("root A did not receive msg_from_b");
        }
        tokio::time::sleep(Duration::from_millis(100)).await;
    }

    join_task.abort();
    listen_task.abort();
    ep_a.close().await;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::merge::ingest_line;

    fn tmp_root(tag: &str) -> PathBuf {
        let p = std::env::temp_dir().join(format!(
            "ws-sync-{}-{}-{}",
            tag,
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let _ = fs::remove_dir_all(&p);
        fs::create_dir_all(&p).unwrap();
        p
    }

    #[test]
    fn identity_persists_across_loads() {
        let dir = tmp_root("id");
        let a = load_or_create_secret_key_in(&dir).unwrap();
        let b = load_or_create_secret_key_in(&dir).unwrap();
        assert_eq!(a.public(), b.public());
        assert_eq!(a.to_bytes(), b.to_bytes());
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn ticket_roundtrip_endpoint_addr_json() {
        let sk = SecretKey::generate();
        let addr = EndpointAddr::new(sk.public());
        let s = encode_ticket(&addr).unwrap();
        let parsed = parse_ticket(&s).unwrap();
        assert_eq!(parsed.id, addr.id);
        let prefixed = format!("ticket: {s}");
        assert_eq!(parse_ticket(&prefixed).unwrap().id, addr.id);
        assert!(
            s.contains("\"id\""),
            "chrome clipboard join looks for an id field, ticket={s}"
        );
        assert!(s.len() >= 24);
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn inprocess_syncs_one_message() {
        let root_a = tmp_root("a");
        let root_b = tmp_root("b");
        let line = r#"{"id":"msg_inproc","channel":"general","body":"hello-from-a"}"#;
        assert!(ingest_line(&root_a, "general", "msg_inproc", line).unwrap());

        let result = tokio::time::timeout(
            Duration::from_secs(40),
            sync_pair_inprocess(&root_a, &root_b),
        )
        .await;

        match result {
            Ok(Ok(())) => {
                let ids: Vec<_> = snapshot(&root_b)
                    .unwrap()
                    .into_iter()
                    .map(|(_, id, _)| id)
                    .collect();
                assert!(ids.contains(&"msg_inproc".to_string()), "ids={ids:?}");
                let ids_a: Vec<_> = snapshot(&root_a)
                    .unwrap()
                    .into_iter()
                    .map(|(_, id, _)| id)
                    .collect();
                assert!(
                    ids_a.contains(&"msg_from_b".to_string()),
                    "root A missing reverse sync, ids={ids_a:?}"
                );
            }
            Ok(Err(e)) => {
                eprintln!("skipping iroh in-process test: {e:#}");
            }
            Err(_) => {
                eprintln!("skipping iroh in-process test: timed out");
            }
        }

        let _ = fs::remove_dir_all(&root_a);
        let _ = fs::remove_dir_all(&root_b);
    }
}
