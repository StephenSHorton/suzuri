//! The frame transport (default; RSZ_TRANSPORT=channel for the Tauri
//! Channel): one WebSocket on 127.0.0.1 carries every pane's frames as
//! binary messages ([u32 pane id][frame]), and the UI's acks come back on it
//! ([u32 pane id][u32 seq]). A Tauri Channel runs an eval plus, above 1 KB, a
//! fetch round trip per message, through the UI thread on Windows; the
//! socket keeps frames off it. Titles, exits and the rest stay on the pane's
//! Channel.
use std::net::TcpListener;
use std::sync::Arc;
use std::time::Duration;

use crossbeam_channel::{Receiver, Sender, unbounded};
use tungstenite::Message;
use tungstenite::handshake::server::{Request, Response};

pub struct FrameBus {
    tx: Sender<Vec<u8>>,
    pub port: u16,
    pub token: String,
}

impl FrameBus {
    pub fn start(on_ack: Arc<dyn Fn(u32, u32) + Send + Sync>) -> std::io::Result<FrameBus> {
        let listener = TcpListener::bind("127.0.0.1:0")?;
        let port = listener.local_addr()?.port();
        let token = format!("{:016x}", std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_nanos() as u64 ^ (std::process::id() as u64) << 32);
        let (tx, rx) = unbounded::<Vec<u8>>();
        let want = format!("/?t={token}");
        std::thread::Builder::new().name("rsz-ws".into()).spawn(move || serve(listener, rx, want, on_ack))?;
        Ok(FrameBus { tx, port, token })
    }

    pub fn send(&self, pane: u32, frame: Vec<u8>) {
        let mut msg = Vec::with_capacity(frame.len() + 4);
        msg.extend_from_slice(&pane.to_le_bytes());
        msg.extend_from_slice(&frame);
        let _ = self.tx.send(msg);
    }
}

fn serve(listener: TcpListener, rx: Receiver<Vec<u8>>, want: String, on_ack: Arc<dyn Fn(u32, u32) + Send + Sync>) {
    for stream in listener.incoming().flatten() {
        let _ = stream.set_nodelay(true);
        // Short reads so one thread can both send frames and take acks.
        let _ = stream.set_read_timeout(Some(Duration::from_millis(2)));
        let check = |req: &Request, resp: Response| {
            if req.uri().to_string() == want {
                Ok(resp)
            } else {
                Err(tungstenite::http::Response::builder().status(403).body(None).unwrap())
            }
        };
        let Ok(mut ws) = tungstenite::accept_hdr(stream, check) else { continue };
        // One window, one socket: serve it until it goes away.
        'conn: loop {
            match rx.recv_timeout(Duration::from_millis(2)) {
                Ok(msg) => {
                    if ws.send(Message::Binary(msg.into())).is_err() {
                        break 'conn;
                    }
                    // Send whatever else is queued before reading.
                    while let Ok(msg) = rx.try_recv() {
                        if ws.send(Message::Binary(msg.into())).is_err() {
                            break 'conn;
                        }
                    }
                }
                Err(crossbeam_channel::RecvTimeoutError::Timeout) => {}
                Err(_) => return,
            }
            loop {
                match ws.read() {
                    Ok(Message::Binary(b)) if b.len() == 8 => {
                        let id = u32::from_le_bytes([b[0], b[1], b[2], b[3]]);
                        let seq = u32::from_le_bytes([b[4], b[5], b[6], b[7]]);
                        on_ack(id, seq);
                    }
                    Ok(Message::Close(_)) => break 'conn,
                    Ok(_) => {}
                    Err(tungstenite::Error::Io(e)) if matches!(e.kind(), std::io::ErrorKind::WouldBlock | std::io::ErrorKind::TimedOut) => break,
                    Err(_) => break 'conn,
                }
            }
        }
    }
}
