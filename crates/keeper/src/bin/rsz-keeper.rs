//! Look at and drive suzuri's kept shells from another program.
//!
//!   rsz-keeper [--dir D] ls                 every kept shell, one per line (JSON)
//!   rsz-keeper [--dir D] screen ID [LINES]  its scrollback tail and screen
//!   rsz-keeper [--dir D] type ID TEXT       type TEXT (\n becomes Enter)
//!   rsz-keeper [--dir D] watch ID [SECS]    attach as a viewer and count frames
//!   rsz-keeper [--dir D] close ID           close the shell (as closing its pane)
//!
//! The folder defaults to suzuri's (see rsz_keeper::dir::default_dir).

fn main() {
    std::process::exit(cli::run());
}

mod cli {
    use std::path::PathBuf;
    use std::sync::Arc;
    use std::sync::atomic::{AtomicU64, Ordering};
    use std::time::Duration;

    use rsz_keeper::{Conn, Event, dir};
    use serde_json::json;

    fn usage() -> i32 {
        eprintln!("usage: rsz-keeper [--dir D] ls | screen ID [LINES] | type ID TEXT | watch ID [SECS] | close ID");
        2
    }

    pub fn run() -> i32 {
        let mut args: Vec<String> = std::env::args().skip(1).collect();
        let mut folder = dir::default_dir();
        if args.first().is_some_and(|a| a == "--dir") && args.len() > 1 {
            folder = PathBuf::from(args.remove(1));
            args.remove(0);
        }
        let Some(cmd) = args.first().cloned() else { return usage() };
        let id = || args.get(1).and_then(|s| s.parse::<u32>().ok());
        let control = |id: u32| Conn::connect(&dir::sock_path(&folder, id), "control", "rsz-keeper", |_| {});
        match cmd.as_str() {
            "ls" => {
                for id in dir::list(&folder) {
                    match control(id) {
                        Ok(c) => println!("{}", c.hello),
                        Err(e) => println!("{}", json!({ "id": id, "error": e.to_string() })),
                    }
                }
                0
            }
            "screen" => {
                let Some(id) = id() else { return usage() };
                let lines: u64 = args.get(2).and_then(|s| s.parse().ok()).unwrap_or(40);
                let c = match control(id) {
                    Ok(c) => c,
                    Err(e) => return fail(e),
                };
                match c.call(json!({ "t": "text", "lines": lines })) {
                    Ok(v) => {
                        for l in v["tail"].as_array().into_iter().flatten() {
                            println!("{}", l.as_str().unwrap_or(""));
                        }
                        println!("{}", v["screen"].as_str().unwrap_or(""));
                        0
                    }
                    Err(e) => fail(e),
                }
            }
            "type" => {
                let (Some(id), Some(text)) = (id(), args.get(2)) else { return usage() };
                let c = match control(id) {
                    Ok(c) => c,
                    Err(e) => return fail(e),
                };
                let bytes = text.replace("\\n", "\r").replace('\n', "\r");
                if let Err(e) = c.input(bytes.as_bytes()) {
                    return fail(e);
                }
                // A round trip, so the bytes are in before the socket closes.
                let _ = c.call(json!({ "t": "state" }));
                0
            }
            "watch" => {
                let Some(id) = id() else { return usage() };
                let secs: f64 = args.get(2).and_then(|s| s.parse().ok()).unwrap_or(3.0);
                let frames = Arc::new(AtomicU64::new(0));
                let bytes = Arc::new(AtomicU64::new(0));
                let (f2, b2) = (frames.clone(), bytes.clone());
                let c = match Conn::connect(&dir::sock_path(&folder, id), "viewer", "rsz-keeper watch", move |e| match e {
                    Event::Frame(f) => {
                        f2.fetch_add(1, Ordering::Relaxed);
                        b2.fetch_add(f.len() as u64, Ordering::Relaxed);
                    }
                    Event::Msg(m) => println!("event {m}"),
                    Event::Closed => println!("closed"),
                }) {
                    Ok(c) => c,
                    Err(e) => return fail(e),
                };
                println!("hello {}", c.hello);
                std::thread::sleep(Duration::from_secs_f64(secs));
                println!("{} frames, {} bytes in {secs} s", frames.load(Ordering::Relaxed), bytes.load(Ordering::Relaxed));
                0
            }
            "close" => {
                let Some(id) = id() else { return usage() };
                match control(id).and_then(|c| c.send(&json!({ "t": "close" }))) {
                    Ok(()) => 0,
                    Err(e) => fail(e),
                }
            }
            _ => usage(),
        }
    }

    fn fail(e: std::io::Error) -> i32 {
        eprintln!("rsz-keeper: {e}");
        1
    }
}
