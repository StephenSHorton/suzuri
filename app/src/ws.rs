//! The frame transport (default; RSZ_TRANSPORT=channel for the Tauri
//! Channel): one WebSocket on 127.0.0.1 carries every pane's frames. Each
//! binary message is one tick: every frame queued when it goes out, from
//! any pane, as [u32 pane id][u32 len][frame] records. The UI's acks come back on it
//! ([u32 pane id][u32 seq]). A Tauri Channel runs an eval plus, above 1 KB, a
//! fetch round trip per message, through the UI thread on Windows; the
//! socket keeps frames off it. Titles, exits and the rest stay on the pane's
//! Channel.
use std::net::TcpListener;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

use crossbeam_channel::{Receiver, Sender, unbounded};
use tungstenite::protocol::Role;
use tungstenite::{Message, WebSocket};
use tungstenite::handshake::server::{Request, Response};

pub struct FrameBus {
    tx: Sender<(u32, Vec<u8>)>,
    pub port: u16,
    pub token: String,
}

impl FrameBus {
    pub fn start(on_ack: Arc<dyn Fn(u32, u32) + Send + Sync>) -> std::io::Result<FrameBus> {
        let listener = TcpListener::bind("127.0.0.1:0")?;
        let port = listener.local_addr()?.port();
        let token = format!("{:016x}", std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_nanos() as u64 ^ (std::process::id() as u64) << 32);
        let (tx, rx) = unbounded::<(u32, Vec<u8>)>();
        let want = format!("/?t={token}");
        std::thread::Builder::new().name("rsz-ws".into()).spawn(move || serve(listener, rx, want, on_ack))?;
        Ok(FrameBus { tx, port, token })
    }

    pub fn send(&self, pane: u32, frame: Vec<u8>) {
        let _ = self.tx.send((pane, frame));
    }
}

fn serve(listener: TcpListener, rx: Receiver<(u32, Vec<u8>)>, want: String, on_ack: Arc<dyn Fn(u32, u32) + Send + Sync>) {
    for stream in listener.incoming().flatten() {
        let _ = stream.set_nodelay(true);
        let check = |req: &Request, resp: Response| {
            if req.uri().to_string() == want {
                Ok(resp)
            } else {
                Err(tungstenite::http::Response::builder().status(403).body(None).unwrap())
            }
        };
        let Ok(mut ws) = tungstenite::accept_hdr(stream, check) else { continue };
        // Acks are read on their own thread with plain blocking reads: no
        // short timeouts anywhere, because on Windows those round up to the
        // ~15.6 ms system tick and a frame waited out one (+7 ms p50).
        let Ok(read_half) = ws.get_ref().try_clone() else { continue };
        let closed = Arc::new(AtomicBool::new(false));
        {
            let closed = closed.clone();
            let on_ack = on_ack.clone();
            let mut reader = WebSocket::from_raw_socket(read_half, Role::Server, None);
            let _ = std::thread::Builder::new().name("rsz-ws-acks".into()).spawn(move || {
                loop {
                    match reader.read() {
                        Ok(Message::Binary(b)) if b.len() == 8 => {
                            let id = u32::from_le_bytes([b[0], b[1], b[2], b[3]]);
                            let seq = u32::from_le_bytes([b[4], b[5], b[6], b[7]]);
                            on_ack(id, seq);
                        }
                        Ok(Message::Close(_)) | Err(_) => break,
                        Ok(_) => {}
                    }
                }
                closed.store(true, Ordering::Relaxed);
            });
        }
        // One window, one socket: frames go out as soon as they're queued
        // (recv wakes on send; the timeout only notices a closed socket),
        // and whatever else is queued by then rides in the same message, so
        // the UI gets one onmessage per tick, not one per pane.
        loop {
            match rx.recv_timeout(Duration::from_millis(250)) {
                Ok(first) => {
                    let mut msg = Vec::with_capacity(first.1.len() + 8);
                    put(&mut msg, first);
                    while let Ok(more) = rx.try_recv() {
                        put(&mut msg, more);
                    }
                    if ws.send(Message::Binary(msg.into())).is_err() {
                        break;
                    }
                }
                Err(crossbeam_channel::RecvTimeoutError::Timeout) => {}
                Err(_) => return,
            }
            if closed.load(Ordering::Relaxed) {
                break;
            }
        }
        let _ = ws.get_ref().shutdown(std::net::Shutdown::Both);
    }
}

/// Append one [u32 pane id][u32 len][frame] record.
fn put(msg: &mut Vec<u8>, (pane, frame): (u32, Vec<u8>)) {
    msg.extend_from_slice(&pane.to_le_bytes());
    msg.extend_from_slice(&(frame.len() as u32).to_le_bytes());
    msg.extend_from_slice(&frame);
}
