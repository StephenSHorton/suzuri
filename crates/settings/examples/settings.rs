//! Read, change and watch the shared settings from a terminal.
//!
//!   cargo run -p rsz-settings --example settings -- get
//!   cargo run -p rsz-settings --example settings -- set font_size_px 18
//!   cargo run -p rsz-settings --example settings -- watch
//!
//! `set` takes a JSON value (a bare word is a string). Run `watch` in one
//! terminal and `set` in another (or change a setting in Suzuri) to see
//! the change arrive.

use rsz_settings::Store;
use serde_json::{Value, json};

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let store = Store::shared();
    match args.iter().map(String::as_str).collect::<Vec<_>>().as_slice() {
        ["get"] | [] => println!("{}", serde_json::to_string_pretty(&store.load()).unwrap()),
        ["get", key] => println!("{}", store.load()[*key]),
        ["set", key, value] => {
            let value = serde_json::from_str::<Value>(value).unwrap_or_else(|_| Value::from(*value));
            match store.update(json!({ *key: value })) {
                Ok(v) => println!("{key} = {}", v[*key]),
                Err(e) => eprintln!("could not save {}: {e}", store.path().display()),
            }
        }
        ["watch"] => {
            println!("watching {}", store.path().display());
            let _w = store.watch(|v| {
                println!(
                    "changed: font {} {}px, cursor {}, look {} {}, profile {}",
                    v["font_face"], v["font_size_px"], v["cursor"], v["ui_theme"], v["ui_mode"], v["active_profile"]
                )
            });
            loop {
                std::thread::park();
            }
        }
        ["meta"] => println!("{}", serde_json::to_string_pretty(&rsz_settings::meta()).unwrap()),
        _ => eprintln!("usage: settings get [key] | set <key> <json> | watch | meta"),
    }
}
