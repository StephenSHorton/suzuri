//! Keep the computer awake from the command line, to try the library:
//!
//!     cargo run -p rsz-caffeine --example stay_awake -- [--shared] [MINUTES]
//!
//! With `--shared`, run it in two terminals: each follows the one switch at
//! `shared_switch_path()`. Type `on`, `on 15`, `off` or `q` and Enter.

use std::io::BufRead;

use rsz_caffeine::{Caffeine, shared_switch_path};

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let shared = args.iter().any(|a| a == "--shared");
    let minutes = args.iter().find_map(|a| a.parse::<u32>().ok());
    let c = if shared {
        let path = shared_switch_path().expect("no home folder");
        println!("shared switch: {}", path.display());
        Caffeine::shared("rsz-caffeine example", path)
    } else {
        Caffeine::solo("rsz-caffeine example")
    };
    c.on_change(|s| {
        println!(
            "caffeine: {} {}",
            if s.active { "on" } else { "off" },
            s.label
        )
    });
    if !shared || minutes.is_some() {
        c.activate(minutes);
    }
    println!("now: {:?}", c.status());
    for line in std::io::stdin().lock().lines() {
        let line = line.unwrap_or_default();
        let mut words = line.split_whitespace();
        match words.next() {
            Some("on") => drop(c.activate(words.next().and_then(|m| m.parse().ok()))),
            Some("off") => drop(c.deactivate()),
            Some("q") | None => break,
            Some(_) => println!("on [minutes] | off | q"),
        }
    }
}
