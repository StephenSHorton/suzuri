//! Optional frame transport (RSZ_TRANSPORT=ws): one WebSocket on 127.0.0.1
//! carries every pane's frames as binary messages ([u32 pane id][frame]).
//! The default Tauri Channel runs an eval plus, above 1 KB, a fetch round
//! trip per message; this is one long-lived socket instead. Titles, exits
//! and the rest stay on the pane's Channel.
use std::net::TcpListener;

use crossbeam_channel::{Receiver, Sender, unbounded};
use tungstenite::Message;
use tungstenite::handshake::server::{Request, Response};

pub struct FrameBus {
    tx: Sender<Vec<u8>>,
    pub port: u16,
    pub token: String,
}

impl FrameBus {
    pub fn start() -> std::io::Result<FrameBus> {
        let listener = TcpListener::bind("127.0.0.1:0")?;
        let port = listener.local_addr()?.port();
        let token = format!("{:016x}", std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_nanos() as u64 ^ (std::process::id() as u64) << 32);
        let (tx, rx) = unbounded::<Vec<u8>>();
        let want = format!("/?t={token}");
        std::thread::Builder::new().name("rsz-ws".into()).spawn(move || serve(listener, rx, want))?;
        Ok(FrameBus { tx, port, token })
    }

    pub fn send(&self, pane: u32, frame: Vec<u8>) {
        let mut msg = Vec::with_capacity(frame.len() + 4);
        msg.extend_from_slice(&pane.to_le_bytes());
        msg.extend_from_slice(&frame);
        let _ = self.tx.send(msg);
    }
}

fn serve(listener: TcpListener, rx: Receiver<Vec<u8>>, want: String) {
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
        // One window, one socket: serve it until it goes away.
        while let Ok(msg) = rx.recv() {
            if ws.send(Message::Binary(msg.into())).is_err() {
                break;
            }
        }
    }
}
