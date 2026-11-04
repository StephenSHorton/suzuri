//! Spawn a command in a pane without any UI, then print its screen.
//! cargo run -p rsz-term --example headless -- [secs] [argv...]
use std::sync::Arc;
use std::time::Duration;

use rsz_term::{Out, Pane, PaneConfig, Sink};

fn main() {
    let mut args: Vec<String> = std::env::args().skip(1).collect();
    let secs: f64 = if args.is_empty() { 3.0 } else { args.remove(0).parse().unwrap_or(3.0) };
    let sink: Sink = Arc::new(|o| match o {
        Out::Frame(f) => eprintln!("frame {} bytes", f.len()),
        Out::Title(t) => eprintln!("title {t}"),
        Out::Exit(c) => eprintln!("exit {c:?}"),
        _ => {}
    });
    let cfg = PaneConfig { cols: 100, rows: 30, command: if args.is_empty() { None } else { Some(args) }, ..Default::default() };
    let pane = Pane::spawn(cfg, sink).expect("spawn");
    std::thread::sleep(Duration::from_secs_f64(secs));
    println!("{}", pane.screen_text());
    pane.close();
    pane.join();
}
