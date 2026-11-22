//! Attaching to a kept shell.
use std::io;
use std::path::Path;
use std::time::Duration;

use serde_json::Value;

/// What the host sends a viewer, besides answers.
pub enum Event {
    /// One rsz-term frame.
    Frame(Vec<u8>),
    /// A JSON event (`"t"`: title, shell, exit, ...).
    Msg(Value),
    /// The connection ended (the host is gone, or [`Conn::detach`]).
    Closed,
}

/// How long a call waits for its answer before giving up: a stuck host
/// must not hang the app.
pub const CALL_TIMEOUT: Duration = Duration::from_secs(2);

pub use imp::Conn;

mod imp {
    use super::*;
    use std::collections::HashMap;
    use std::sync::Arc;
    use std::sync::atomic::{AtomicU64, Ordering};
    use std::sync::mpsc::{Sender, channel};

    use parking_lot_free::Mutex;

    use crate::sock::UnixStream;
    use crate::{K_FRAME, K_INPUT, K_JSON, PROTO, wire};

    /// One connection to a host.
    pub struct Conn {
        stream: UnixStream,
        writer: Mutex<UnixStream>,
        pending: Arc<Mutex<HashMap<u64, Sender<Value>>>>,
        next: AtomicU64,
        /// The host's hello.
        pub hello: Value,
    }

    impl Conn {
        /// Connect as `role` ("viewer": events and frames; "control":
        /// answers only). `on` runs on the connection's reader thread.
        pub fn connect(path: &Path, role: &str, client: &str, on: impl Fn(Event) + Send + 'static) -> io::Result<Conn> {
            let mut stream = UnixStream::connect(path)?;
            if !crate::dir::peer_is_me(&stream) {
                return Err(io::Error::new(io::ErrorKind::PermissionDenied, "keeper: the socket's host is another user's"));
            }
            stream.set_read_timeout(Some(Duration::from_secs(5)))?;
            let hi = serde_json::json!({ "t": "hello", "proto": PROTO, "role": role, "client": client });
            wire::write(&mut stream, K_JSON, hi.to_string().as_bytes())?;
            let (k, b) = wire::read(&mut stream)?;
            let hello = wire::json(k, &b)?;
            if hello["t"] != "hello" {
                return Err(io::Error::new(io::ErrorKind::InvalidData, "keeper: no hello"));
            }
            let min = hello["min"].as_u64().unwrap_or(1);
            if min > PROTO as u64 {
                return Err(io::Error::new(io::ErrorKind::Unsupported, format!("keeper: the host needs protocol {min}, this is {PROTO}")));
            }
            stream.set_read_timeout(None)?;
            let pending: Arc<Mutex<HashMap<u64, Sender<Value>>>> = Arc::default();
            let mut reader = stream.try_clone()?;
            {
                let pending = pending.clone();
                std::thread::Builder::new().name("rsz-keeper-read".into()).spawn(move || {
                    loop {
                        match wire::read(&mut reader) {
                            Ok((K_FRAME, b)) => on(Event::Frame(b)),
                            Ok((K_JSON, b)) => {
                                let Ok(v) = serde_json::from_slice::<Value>(&b) else { continue };
                                if v["t"] == "r" {
                                    if let Some(tx) = v["q"].as_u64().and_then(|q| pending.lock().remove(&q)) {
                                        let _ = tx.send(v["v"].clone());
                                    }
                                } else {
                                    on(Event::Msg(v));
                                }
                            }
                            Ok(_) => {}
                            Err(_) => break,
                        }
                    }
                    pending.lock().clear();
                    on(Event::Closed);
                })?;
            }
            let writer = Mutex::new(stream.try_clone()?);
            Ok(Conn { stream, writer, pending, next: AtomicU64::new(1), hello })
        }

        pub fn send(&self, v: &Value) -> io::Result<()> {
            use std::io::Write;
            self.writer.lock().write_all(&wire::encode_json(v))
        }

        /// Bytes typed into the shell.
        pub fn input(&self, bytes: &[u8]) -> io::Result<()> {
            use std::io::Write;
            self.writer.lock().write_all(&wire::encode(K_INPUT, bytes))
        }

        /// A request and its answer (Null when the host gave none in time).
        pub fn call(&self, mut v: Value) -> io::Result<Value> {
            let q = self.next.fetch_add(1, Ordering::Relaxed);
            v["q"] = q.into();
            let (tx, rx) = channel();
            self.pending.lock().insert(q, tx);
            if let Err(e) = self.send(&v) {
                self.pending.lock().remove(&q);
                return Err(e);
            }
            match rx.recv_timeout(CALL_TIMEOUT) {
                Ok(v) => Ok(v),
                Err(_) => {
                    self.pending.lock().remove(&q);
                    Err(io::Error::new(io::ErrorKind::TimedOut, "keeper: no answer"))
                }
            }
        }

        /// Leave the shell running and end this connection.
        pub fn detach(&self) {
            let _ = self.stream.shutdown(std::net::Shutdown::Both);
        }
    }

    impl Drop for Conn {
        fn drop(&mut self) {
            self.detach();
        }
    }

    /// std's Mutex without poisoning, so a panicked callback can't wedge a pane.
    mod parking_lot_free {
        pub struct Mutex<T>(std::sync::Mutex<T>);
        impl<T: Default> Default for Mutex<T> {
            fn default() -> Self {
                Mutex(std::sync::Mutex::new(T::default()))
            }
        }
        impl<T> Mutex<T> {
            pub fn new(t: T) -> Self {
                Mutex(std::sync::Mutex::new(t))
            }
            pub fn lock(&self) -> std::sync::MutexGuard<'_, T> {
                self.0.lock().unwrap_or_else(|e| e.into_inner())
            }
        }
    }
}
