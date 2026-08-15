//! Per-pane localhost JSON channel to a guest process.
//!
//! Chrome binds `127.0.0.1:0`, spawns the manifest command with
//! `--suzuri-guest --port N`, then speaks protocol v1 as one JSON object per
//! line. Chrome never links the guest engine.

use std::collections::HashMap;
use std::io::{BufRead, BufReader, Write};
use std::net::TcpListener;
use std::process::{Child, Command, Stdio};
use std::sync::mpsc::{self, Receiver, Sender, TryRecvError};
use std::thread;
use std::time::Duration;

use crate::guest_fb;
use crate::guest_manifest::GuestManifest;
use crate::layout::Rect;

/// Mac attach: guest paints a borderless window over this screen rect,
/// ordered above `window_number`. Cross-process; not an NSView subview.
#[derive(Clone, Copy, Debug)]
pub struct NativeAttach {
    pub window_number: i64,
    pub screen: Rect,
    pub visible: bool,
}

/// Guest pixels fill the cell well (below the mosaic header). Empty if tiny.
pub fn guest_hole_rect(cells: Rect) -> Rect {
    if cells.w < 24.0 || cells.h < 24.0 {
        return Rect::new(cells.x, cells.y, 0.0, 0.0);
    }
    cells
}

/// Web view fills the pane under the header and stops at the footer
/// (`footer_top`). Corners clip to the glass in the blit shader.
pub fn guest_mount_rect(glass: Rect, header: Rect, footer_top: f32) -> Rect {
    let top = if header.h > 1.0 {
        header.y + header.h
    } else {
        glass.y
    };
    let y = top.clamp(glass.y, glass.y + glass.h);
    let bottom = if footer_top > y + 8.0 {
        footer_top
    } else {
        glass.y + glass.h
    };
    guest_hole_rect(Rect::new(
        glass.x,
        y,
        glass.w.max(0.0),
        (bottom - y).max(0.0),
    ))
}

pub fn guest_footer_top(divider: Rect, glass: Rect) -> f32 {
    if divider.h > 0.5 {
        divider.y
    } else {
        glass.y + glass.h
    }
}

/// Events from a guest, tagged with the pane that owns the process.
#[derive(Clone, Debug, PartialEq)]
pub enum GuestEvent {
    Hello {
        pane_id: u64,
        protocol: u32,
        title: String,
    },
    Title {
        pane_id: u64,
        text: String,
    },
    Url {
        pane_id: u64,
        text: String,
    },
    Busy {
        pane_id: u64,
        busy: bool,
    },
    Crash {
        pane_id: u64,
        message: String,
    },
    Surface {
        pane_id: u64,
        path: String,
        width: u32,
        height: u32,
    },
}

struct LiveGuest {
    #[allow(dead_code)]
    guest_id: String,
    child: Child,
    out_tx: Sender<String>,
    last_geom: Option<GeomKey>,
    fb_path: std::path::PathBuf,
    fb_seq: u32,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
struct GeomKey {
    x: i32,
    y: i32,
    w: i32,
    h: i32,
    scale_cents: u32,
    win: i64,
    sx: i32,
    sy: i32,
    sw: i32,
    sh: i32,
    visible: bool,
}

fn geom_key(rect: Rect, scale: f32, native: Option<&NativeAttach>) -> GeomKey {
    let (win, sx, sy, sw, sh, visible) = match native {
        Some(n) => (
            n.window_number,
            n.screen.x.round() as i32,
            n.screen.y.round() as i32,
            n.screen.w.round() as i32,
            n.screen.h.round() as i32,
            n.visible,
        ),
        None => (0, 0, 0, 0, 0, true),
    };
    GeomKey {
        x: rect.x.round() as i32,
        y: rect.y.round() as i32,
        w: rect.w.round() as i32,
        h: rect.h.round() as i32,
        scale_cents: (scale * 100.0).round() as u32,
        win,
        sx,
        sy,
        sw,
        sh,
        visible,
    }
}

/// Owns guest processes and a channel of inbound protocol events.
pub struct GuestHost {
    events_tx: Sender<GuestEvent>,
    events_rx: Receiver<GuestEvent>,
    live: HashMap<u64, LiveGuest>,
}

impl Default for GuestHost {
    fn default() -> Self {
        Self::new()
    }
}

impl GuestHost {
    pub fn new() -> Self {
        let (events_tx, events_rx) = mpsc::channel();
        Self {
            events_tx,
            events_rx,
            live: HashMap::new(),
        }
    }

    /// Drain inbound messages (non-blocking). Reaps dead children as crash.
    pub fn poll(&mut self) -> Vec<GuestEvent> {
        let mut out = Vec::new();
        loop {
            match self.events_rx.try_recv() {
                Ok(ev) => out.push(ev),
                Err(TryRecvError::Empty) => break,
                Err(TryRecvError::Disconnected) => break,
            }
        }
        let mut dead = Vec::new();
        for (id, live) in self.live.iter_mut() {
            match live.child.try_wait() {
                Ok(Some(status)) => {
                    if !matches_crash(&out, *id) {
                        let message = if status.success() {
                            "guest exited".into()
                        } else {
                            format!("guest exited ({status})")
                        };
                        dead.push((*id, message));
                    } else {
                        dead.push((*id, String::new()));
                    }
                }
                Ok(None) => {}
                Err(e) => dead.push((*id, e.to_string())),
            }
        }
        for (id, message) in dead {
            self.live.remove(&id);
            if !message.is_empty() && !matches_crash(&out, id) {
                out.push(GuestEvent::Crash {
                    pane_id: id,
                    message,
                });
            }
        }
        out
    }

    pub fn is_live(&self, pane_id: u64) -> bool {
        self.live.contains_key(&pane_id)
    }

    pub fn live_pane_ids(&self) -> Vec<u64> {
        self.live.keys().copied().collect()
    }

    /// Spawn the guest binary and send `spawn` after it connects.
    pub fn start(
        &mut self,
        pane_id: u64,
        manifest: &GuestManifest,
        cwd: &str,
        rect: Rect,
        scale: f32,
        native: Option<NativeAttach>,
    ) -> Result<(), String> {
        if self.live.contains_key(&pane_id) {
            return Ok(());
        }
        let listener = TcpListener::bind("127.0.0.1:0").map_err(|e| e.to_string())?;
        listener
            .set_nonblocking(false)
            .map_err(|e| e.to_string())?;
        let port = listener.local_addr().map_err(|e| e.to_string())?.port();

        let mut cmd = Command::new(&manifest.command);
        cmd.arg("--suzuri-guest")
            .arg("--port")
            .arg(port.to_string())
            .env("SUZURI_GUEST_PORT", port.to_string())
            .env("SUZURI_PANE_ID", pane_id.to_string())
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::piped());
        for a in &manifest.args {
            cmd.arg(a);
        }
        let child = cmd.spawn().map_err(|e| {
            format!(
                "spawn {}: {e}",
                manifest.command.display()
            )
        })?;

        let (fb_w, fb_h) = guest_fb::pixel_size(rect.w, rect.h, scale);
        let fb_path = guest_fb::fb_path(pane_id);
        let _ = guest_fb::create(&fb_path, fb_w, fb_h);
        let ev_tx = self.events_tx.clone();
        let spawn_line = encode_spawn(
            pane_id,
            rect,
            scale,
            cwd,
            native.as_ref(),
            Some((&fb_path, fb_w, fb_h)),
        );
        let (out_tx, out_rx) = mpsc::channel::<String>();
        let (ready_tx, ready_rx) = mpsc::channel::<Result<(), String>>();

        thread::spawn(move || {
            if let Err(e) = listener.set_nonblocking(true) {
                let _ = ready_tx.send(Err(e.to_string()));
                return;
            }
            let stream = match accept_timeout(&listener, Duration::from_secs(5)) {
                Ok(s) => s,
                Err(e) => {
                    let _ = ready_tx.send(Err(e));
                    return;
                }
            };
            // Listener is nonblocking; accepted fds inherit that on macOS.
            if let Err(e) = stream.set_nonblocking(false) {
                let _ = ready_tx.send(Err(e.to_string()));
                return;
            }
            if let Err(e) = stream.set_read_timeout(Some(Duration::from_millis(50))) {
                let _ = ready_tx.send(Err(e.to_string()));
                return;
            }
            let mut stream = stream;
            if writeln!(stream, "{spawn_line}").is_err() {
                let _ = ready_tx.send(Err("failed to send spawn".into()));
                return;
            }
            let _ = stream.flush();
            let _ = ready_tx.send(Ok(()));

            let mut reader = BufReader::new(stream);
            loop {
                while let Ok(line) = out_rx.try_recv() {
                    if writeln!(reader.get_mut(), "{line}").is_err() {
                        let _ = ev_tx.send(GuestEvent::Crash {
                            pane_id,
                            message: "guest socket write failed".into(),
                        });
                        return;
                    }
                    let _ = reader.get_mut().flush();
                    if line.contains("\"type\":\"kill\"") {
                        return;
                    }
                }
                let mut buf = String::new();
                match reader.read_line(&mut buf) {
                    Ok(0) => break,
                    Ok(_) => {
                        let line = buf.trim();
                        if line.is_empty() {
                            continue;
                        }
                        if let Some(ev) = decode_guest_line(pane_id, line) {
                            if ev_tx.send(ev).is_err() {
                                break;
                            }
                        }
                    }
                    Err(e)
                        if e.kind() == std::io::ErrorKind::WouldBlock
                            || e.kind() == std::io::ErrorKind::TimedOut => {}
                    Err(_) => break,
                }
            }
        });

        match ready_rx.recv_timeout(Duration::from_secs(6)) {
            Ok(Ok(())) => {}
            Ok(Err(e)) => {
                let mut child = child;
                let _ = child.kill();
                return Err(e);
            }
            Err(_) => {
                let mut child = child;
                let _ = child.kill();
                return Err("guest did not connect".into());
            }
        }

        self.live.insert(
            pane_id,
            LiveGuest {
                guest_id: manifest.id.clone(),
                child,
                out_tx,
                last_geom: Some(geom_key(rect, scale, native.as_ref())),
                fb_path,
                fb_seq: 0,
            },
        );
        Ok(())
    }

    pub fn navigate(&self, pane_id: u64, url: &str) {
        self.send(pane_id, &encode_navigate(url));
    }

    /// Scroll the guest document. `dx`/`dy` are CSS pixels, same sign as
    /// Ladybird (`-scrollingDelta`). `x`/`y` are the pointer in the well.
    pub fn scroll(&self, pane_id: u64, dx: f64, dy: f64, x: f32, y: f32) {
        if (!dx.is_finite() || dx.abs() < 1e-4) && (!dy.is_finite() || dy.abs() < 1e-4) {
            return;
        }
        self.send(pane_id, &encode_scroll(dx, dy, x, y));
    }

    /// Pointer in the well, CSS/logical px relative to the hole origin.
    pub fn pointer(
        &self,
        pane_id: u64,
        kind: &str,
        x: f32,
        y: f32,
        button: i32,
        buttons: i32,
        modifiers: i32,
    ) {
        self.send(
            pane_id,
            &encode_pointer(kind, x, y, button, buttons, modifiers),
        );
    }

    /// Key to the guest document. `kind` is `down` or `up`.
    pub fn key(&self, pane_id: u64, kind: &str, key: &str, text: &str, modifiers: i32) {
        self.send(pane_id, &encode_key(kind, key, text, modifiers));
    }

    pub fn draft(&self, pane_id: u64, text: &str) {
        self.send(pane_id, &encode_draft(text));
    }

    pub fn restack(&self, pane_id: u64) {
        self.send(pane_id, r#"{"type":"stack"}"#);
    }

    pub fn resize(&mut self, pane_id: u64, rect: Rect, scale: f32, native: Option<NativeAttach>) {
        let key = geom_key(rect, scale, native.as_ref());
        let line = {
            let Some(live) = self.live.get_mut(&pane_id) else {
                self.send(pane_id, &encode_resize(rect, scale, native.as_ref(), None));
                return;
            };
            if live.last_geom == Some(key) {
                return;
            }
            live.last_geom = Some(key);
            let path = live.fb_path.clone();
            let (fb_w, fb_h) = guest_fb::pixel_size(rect.w, rect.h, scale);
            // Same pixel size: do not recreate the file (truncating SIGBUS's
            // a guest that still has the well mmap'd).
            let _ = guest_fb::create(&path, fb_w, fb_h);
            encode_resize(rect, scale, native.as_ref(), Some((&path, fb_w, fb_h)))
        };
        self.send(pane_id, &line);
    }

    /// Copy guest pixels if the file seq advanced.
    pub fn take_fb(&mut self, pane_id: u64) -> Option<(u32, u32, Vec<u8>)> {
        let live = self.live.get_mut(&pane_id)?;
        let (w, h, seq, px) = guest_fb::read_if_newer(&live.fb_path, live.fb_seq)
            .ok()
            .flatten()?;
        live.fb_seq = seq;
        Some((w, h, px))
    }

    /// True when the file seq is ahead of the last upload (wake a paint).
    pub fn fb_newer(&self, pane_id: u64) -> bool {
        let Some(live) = self.live.get(&pane_id) else {
            return false;
        };
        guest_fb::peek_seq(&live.fb_path).is_some_and(|s| s != live.fb_seq)
    }

    pub fn focus(&self, pane_id: u64, inn: bool) {
        self.send(pane_id, &encode_focus(inn));
    }

    /// Ask the guest to exit, then drop the handle. Force-kill on drop of Child.
    pub fn kill(&mut self, pane_id: u64) {
        if let Some(live) = self.live.remove(&pane_id) {
            guest_fb::remove(&live.fb_path);
            let _ = live.out_tx.send(encode_kill());
            let mut child = live.child;
            thread::spawn(move || {
                thread::sleep(Duration::from_millis(250));
                let _ = child.kill();
                let _ = child.wait();
            });
        }
    }

    fn send(&self, pane_id: u64, line: &str) {
        if let Some(live) = self.live.get(&pane_id) {
            let _ = live.out_tx.send(line.to_string());
        }
    }
}

fn matches_crash(events: &[GuestEvent], pane_id: u64) -> bool {
    events.iter().any(|e| matches!(e, GuestEvent::Crash { pane_id: id, .. } if *id == pane_id))
}

fn accept_timeout(
    listener: &TcpListener,
    budget: Duration,
) -> Result<std::net::TcpStream, String> {
    let start = std::time::Instant::now();
    loop {
        match listener.accept() {
            Ok((s, _)) => {
                let _ = s.set_nodelay(true);
                return Ok(s);
            }
            Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                if start.elapsed() >= budget {
                    return Err("guest did not connect".into());
                }
                thread::sleep(Duration::from_millis(20));
            }
            Err(e) => return Err(e.to_string()),
        }
    }
}

fn json_escape(s: &str) -> String {
    let mut o = String::new();
    for c in s.chars() {
        match c {
            '"' => o.push_str("\\\""),
            '\\' => o.push_str("\\\\"),
            '\n' => o.push_str("\\n"),
            '\r' => o.push_str("\\r"),
            '\t' => o.push_str("\\t"),
            c if c.is_control() => o.push_str(&format!("\\u{:04x}", c as u32)),
            c => o.push(c),
        }
    }
    o
}

fn encode_rect(rect: Rect) -> String {
    format!(
        r#"{{"x":{},"y":{},"w":{},"h":{}}}"#,
        rect.x, rect.y, rect.w, rect.h
    )
}

fn encode_native_suffix(native: Option<&NativeAttach>) -> String {
    match native {
        None => String::new(),
        Some(n) => format!(
            r#","visible":{},"native":{{"kind":"nswindow","window_number":{},"screen":{}}}"#,
            if n.visible { "true" } else { "false" },
            n.window_number,
            encode_rect(n.screen)
        ),
    }
}

fn encode_fb_suffix(fb: Option<(&std::path::PathBuf, u32, u32)>) -> String {
    match fb {
        None => String::new(),
        Some((path, w, h)) => format!(
            r#","fb":{{"path":"{}","width":{w},"height":{h}}}"#,
            json_escape(&path.to_string_lossy())
        ),
    }
}

fn encode_spawn(
    pane_id: u64,
    rect: Rect,
    scale: f32,
    cwd: &str,
    native: Option<&NativeAttach>,
    fb: Option<(&std::path::PathBuf, u32, u32)>,
) -> String {
    format!(
        r#"{{"type":"spawn","pane_id":"{pane_id}","rect":{},"scale":{scale},"cwd":"{}"{}{}}}"#,
        encode_rect(rect),
        json_escape(cwd),
        encode_native_suffix(native),
        encode_fb_suffix(fb)
    )
}

fn encode_resize(
    rect: Rect,
    scale: f32,
    native: Option<&NativeAttach>,
    fb: Option<(&std::path::PathBuf, u32, u32)>,
) -> String {
    format!(
        r#"{{"type":"resize","rect":{},"scale":{scale}{}{}}}"#,
        encode_rect(rect),
        encode_native_suffix(native),
        encode_fb_suffix(fb)
    )
}

fn encode_focus(inn: bool) -> String {
    format!(r#"{{"type":"focus","in":{}}}"#, if inn { "true" } else { "false" })
}

fn encode_navigate(url: &str) -> String {
    format!(r#"{{"type":"navigate","url":"{}"}}"#, json_escape(url))
}

fn encode_scroll(dx: f64, dy: f64, x: f32, y: f32) -> String {
    format!(
        r#"{{"type":"scroll","dx":{dx:.3},"dy":{dy:.3},"x":{x:.2},"y":{y:.2}}}"#
    )
}

fn encode_pointer(kind: &str, x: f32, y: f32, button: i32, buttons: i32, modifiers: i32) -> String {
    format!(
        r#"{{"type":"pointer","kind":"{kind}","x":{x:.2},"y":{y:.2},"button":{button},"buttons":{buttons},"modifiers":{modifiers}}}"#
    )
}

fn encode_key(kind: &str, key: &str, text: &str, modifiers: i32) -> String {
    format!(
        r#"{{"type":"key","kind":"{kind}","key":"{}","text":"{}","modifiers":{modifiers}}}"#,
        json_escape(key),
        json_escape(text),
    )
}

fn encode_draft(text: &str) -> String {
    format!(r#"{{"type":"draft","string":"{}"}}"#, json_escape(text))
}

fn encode_kill() -> String {
    r#"{"type":"kill"}"#.into()
}

fn decode_guest_line(pane_id: u64, line: &str) -> Option<GuestEvent> {
    let v: serde_json::Value = serde_json::from_str(line).ok()?;
    let ty = v.get("type")?.as_str()?;
    match ty {
        "hello" => Some(GuestEvent::Hello {
            pane_id,
            protocol: v.get("protocol").and_then(|x| x.as_u64()).unwrap_or(1) as u32,
            title: v
                .get("title")
                .and_then(|x| x.as_str())
                .unwrap_or("guest")
                .to_string(),
        }),
        "title" => Some(GuestEvent::Title {
            pane_id,
            text: string_field(&v),
        }),
        "url" => Some(GuestEvent::Url {
            pane_id,
            text: string_field(&v),
        }),
        "busy" => Some(GuestEvent::Busy {
            pane_id,
            busy: v
                .get("bool")
                .or_else(|| v.get("busy"))
                .and_then(|x| x.as_bool())
                .unwrap_or(false),
        }),
        "crash" => Some(GuestEvent::Crash {
            pane_id,
            message: v
                .get("message")
                .and_then(|x| x.as_str())
                .unwrap_or("guest crashed")
                .to_string(),
        }),
        "surface" => Some(GuestEvent::Surface {
            pane_id,
            path: v
                .get("path")
                .and_then(|x| x.as_str())
                .unwrap_or("")
                .to_string(),
            width: v.get("width").and_then(|x| x.as_u64()).unwrap_or(0) as u32,
            height: v.get("height").and_then(|x| x.as_u64()).unwrap_or(0) as u32,
        }),
        _ => None,
    }
}

fn string_field(v: &serde_json::Value) -> String {
    v.get("string")
        .or_else(|| v.get("title"))
        .or_else(|| v.get("url"))
        .and_then(|x| x.as_str())
        .unwrap_or("")
        .to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn decode_hello_title_url() {
        let h = decode_guest_line(3, r#"{"type":"hello","protocol":1,"title":"Example"}"#)
            .unwrap();
        assert_eq!(
            h,
            GuestEvent::Hello {
                pane_id: 3,
                protocol: 1,
                title: "Example".into()
            }
        );
        let t = decode_guest_line(3, r#"{"type":"title","string":"https://x.test"}"#).unwrap();
        assert_eq!(
            t,
            GuestEvent::Title {
                pane_id: 3,
                text: "https://x.test".into()
            }
        );
        let u = decode_guest_line(3, r#"{"type":"url","string":"https://x.test"}"#).unwrap();
        assert_eq!(
            u,
            GuestEvent::Url {
                pane_id: 3,
                text: "https://x.test".into()
            }
        );
    }

    #[test]
    fn encode_roundtrip_shapes() {
        let line = encode_navigate("https://a.test/q?x=1");
        assert!(line.contains(r#""type":"navigate""#));
        assert!(line.contains("https://a.test/q?x=1"));
        let sc = encode_scroll(1.25, -4.5, 10.0, 20.0);
        assert!(sc.contains(r#""type":"scroll""#));
        assert!(sc.contains(r#""dy":-4.500"#));
        assert!(sc.contains(r#""x":10.00"#));
        let p = encode_pointer("down", 10.0, 20.5, 0, 1, 0);
        assert!(p.contains(r#""type":"pointer""#));
        assert!(p.contains(r#""kind":"down""#));
        assert!(p.contains(r#""y":20.50"#));
        let k = encode_key("down", "Enter", "", 0);
        assert!(k.contains(r#""type":"key""#));
        assert!(k.contains(r#""key":"Enter""#));
        let s = encode_spawn(9, Rect::new(1.0, 2.0, 3.0, 4.0), 2.0, "/tmp", None, None);
        assert!(s.contains(r#""type":"spawn""#));
        assert!(s.contains(r#""pane_id":"9""#));
        assert!(!s.contains("native"));
        let n = NativeAttach {
            window_number: 42,
            screen: Rect::new(10.0, 20.0, 30.0, 40.0),
            visible: true,
        };
        let with = encode_spawn(9, Rect::new(1.0, 2.0, 3.0, 4.0), 2.0, "/tmp", Some(&n), None);
        assert!(with.contains(r#""window_number":42"#));
        assert!(with.contains(r#""kind":"nswindow""#));
        assert!(with.contains(r#""visible":true"#));
        let path = std::path::PathBuf::from("/tmp/x.szfb");
        let fb = encode_spawn(
            1,
            Rect::new(0.0, 0.0, 10.0, 10.0),
            1.0,
            "/",
            None,
            Some((&path, 20, 10)),
        );
        assert!(fb.contains(r#""fb":{"path":"/tmp/x.szfb""#));
        assert!(fb.contains(r#""width":20"#));
        assert!(fb.contains(r#""height":10"#));
    }

    #[test]
    fn hole_is_the_cell_well() {
        let cells = Rect::new(8.0, 30.0, 400.0, 260.0);
        assert_eq!(guest_hole_rect(cells).w, 400.0);
        assert_eq!(guest_hole_rect(Rect::new(0.0, 0.0, 10.0, 10.0)).w, 0.0);
        let glass = Rect::new(0.0, 0.0, 400.0, 300.0);
        let header = Rect::new(8.0, 4.0, 384.0, 22.0);
        let m = guest_mount_rect(glass, header, 260.0);
        assert!((m.y - (header.y + header.h)).abs() < 0.01);
        assert!((m.x - glass.x).abs() < 0.01);
        assert!((m.w - glass.w).abs() < 0.01);
        assert!((m.y + m.h - 260.0).abs() < 0.01);
    }

    #[test]
    fn mock_guest_speaks_protocol() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        let handle = thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            writeln!(stream, r#"{{"type":"hello","protocol":1,"title":"Example"}}"#).unwrap();
            let mut reader = BufReader::new(stream.try_clone().unwrap());
            let mut line = String::new();
            reader.read_line(&mut line).unwrap();
            assert!(line.contains("spawn"));
            line.clear();
            reader.read_line(&mut line).unwrap();
            assert!(line.contains("navigate"));
            writeln!(stream, r#"{{"type":"title","string":"echoed"}}"#).unwrap();
            writeln!(stream, r#"{{"type":"url","string":"echoed"}}"#).unwrap();
        });

        let mut stream = std::net::TcpStream::connect(("127.0.0.1", port)).unwrap();
        let mut reader = BufReader::new(stream.try_clone().unwrap());
        let mut hello = String::new();
        reader.read_line(&mut hello).unwrap();
        let ev = decode_guest_line(1, hello.trim()).unwrap();
        assert!(matches!(ev, GuestEvent::Hello { title, .. } if title == "Example"));
        writeln!(
            stream,
            "{}",
            encode_spawn(1, Rect::new(0.0, 0.0, 10.0, 10.0), 1.0, "/", None, None)
        )
        .unwrap();
        writeln!(stream, "{}", encode_navigate("echoed")).unwrap();
        let mut title = String::new();
        reader.read_line(&mut title).unwrap();
        assert!(matches!(
            decode_guest_line(1, title.trim()),
            Some(GuestEvent::Title { text, .. }) if text == "echoed"
        ));
        handle.join().unwrap();
    }

    #[test]
    fn example_binary_echoes_navigate() {
        let bin = build_example_binary();
        assert!(bin.is_file(), "missing {}", bin.display());
        let manifest = GuestManifest {
            id: "example".into(),
            name: "Example".into(),
            command: bin,
            protocol: 1,
            capabilities: vec!["pane".into(), "navigate".into()],
            args: vec![],
            path: std::path::PathBuf::from("example.json"),
        };
        let mut host = GuestHost::new();
        host.start(
            7,
            &manifest,
            "/tmp",
            Rect::new(0.0, 0.0, 320.0, 200.0),
            1.0,
            None,
        )
        .expect("start example");

        let mut saw_hello = false;
        let mut saw_surface = false;
        let start = std::time::Instant::now();
        while start.elapsed() < Duration::from_millis(2000) {
            for ev in host.poll() {
                match &ev {
                    GuestEvent::Hello {
                        pane_id: 7, title, ..
                    } if title == "Example" => saw_hello = true,
                    GuestEvent::Surface {
                        pane_id: 7,
                        width,
                        height,
                        ..
                    } if *width > 0 && *height > 0 => saw_surface = true,
                    _ => {}
                }
            }
            if saw_hello && saw_surface {
                break;
            }
            thread::sleep(Duration::from_millis(15));
        }
        assert!(saw_hello, "no hello from example guest");
        assert!(saw_surface, "example did not announce a surface");
        host.navigate(7, "https://echo.test/");
        let echoed = wait_event(&mut host, 2000, |e| {
            matches!(e, GuestEvent::Url { pane_id: 7, text } if text == "https://echo.test/")
        });
        assert!(echoed, "example did not echo navigate");
        let (w, h, px) = host.take_fb(7).expect("example framebuffer");
        assert!(w >= 8 && h >= 8, "fb {w}x{h}");
        assert_eq!(&px[0..4], &[74, 186, 61, 255], "left rail should be guest jade");
        host.kill(7);
    }

    fn build_example_binary() -> std::path::PathBuf {
        let manifest = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../guests/example/Cargo.toml");
        let status = std::process::Command::new("cargo")
            .args(["build", "--quiet", "--manifest-path"])
            .arg(&manifest)
            .status()
            .expect("cargo build example");
        assert!(status.success(), "example crate failed to build");
        let mut bin = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../guests/example/target/debug/suzuri-guest-example");
        if !std::env::consts::EXE_SUFFIX.is_empty() {
            bin.set_extension(std::env::consts::EXE_EXTENSION);
        }
        bin
    }

    fn wait_event(
        host: &mut GuestHost,
        timeout_ms: u64,
        pred: impl Fn(&GuestEvent) -> bool,
    ) -> bool {
        let start = std::time::Instant::now();
        while start.elapsed() < Duration::from_millis(timeout_ms) {
            for ev in host.poll() {
                if pred(&ev) {
                    return true;
                }
            }
            thread::sleep(Duration::from_millis(15));
        }
        false
    }
}
