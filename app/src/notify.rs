//! `suzuri notify` (Go suzuri's `suzuri notify`, internal/ui/notify_cli.go):
//! play the editor notification sounds, or show the notice cards a terminal
//! program would get, without opening a terminal window.
//!
//!   notify                 play success, fail and published in turn
//!   notify fail published  play those
//!   notify show            one card of each kind, with its sound
//!   notify show fail "Compile failed" "main.go:12"
//!   notify show update     the update offer (Install / Skip)

use std::time::{Duration, Instant};

use rsz_term::protocols::Notice;
use tauri::Manager;

pub const USAGE: &str = "usage: suzuri notify [success|fail|published|all]
       suzuri notify show [success|fail|published|update] [title] [body]

  notify                 play the three sounds
  notify show            show one card of each kind, with its sound
  notify show fail \"Compile failed\" \"main.go:12\"
  notify show update [v] the update offer: Install and Skip buttons
";

const SUCCESS: &[u8] = include_bytes!("../../ui/sounds/success.wav");
const FAIL: &[u8] = include_bytes!("../../ui/sounds/fail.wav");
const PUBLISHED: &[u8] = include_bytes!("../../ui/sounds/published.wav");

/// Whether argv asks for the notify CLI (Go accepts `notify` and `sound`).
pub fn wants(args: &[String]) -> bool {
    matches!(args.get(1).map(String::as_str), Some("notify" | "sound"))
}

/// Go's notifySoundArgs: which sounds, in order.
pub fn sound_args(args: &[String]) -> Result<Vec<&'static str>, String> {
    if args.is_empty() {
        return Ok(vec!["success", "fail", "published"]);
    }
    if args.len() == 1 && matches!(args[0].as_str(), "-h" | "--help") {
        return Err("help".into());
    }
    let mut out = Vec::new();
    for a in args {
        match a.trim().to_ascii_lowercase().as_str() {
            "" | "all" => out.extend(["success", "fail", "published"]),
            "success" | "info" | "system" => out.push("success"),
            "fail" | "error" => out.push("fail"),
            "published" | "warn" | "warning" | "question" => out.push("published"),
            _ => return Err(format!("unknown sound {a:?}")),
        }
    }
    Ok(out)
}

fn note(title: &str, body: &str, sound: &str, icon: &str, urgency: u8, expire_ms: u32, id: &str) -> Notice {
    Notice {
        id: id.into(),
        title: title.into(),
        body: body.into(),
        sound: sound.into(),
        icon: icon.into(),
        urgency,
        occasion: "always".into(),
        expire_ms: Some(expire_ms),
        report: false,
        focus: false,
        close_event: false,
    }
}

/// Go's sampleNote: 4 s cards; fail is critical with the error icon.
fn sample(kind: &str, title: &str, body: &str) -> Notice {
    match kind {
        "fail" | "error" => note(title, body, "error", "error", 2, 4000, ""),
        "published" | "warn" | "warning" | "question" => note(title, body, "question", "", 1, 4000, ""),
        _ => note(title, body, "info", "", 1, 4000, ""),
    }
}

/// Go's notifyShowArgs.
pub fn show_args(args: &[String]) -> Result<Vec<Notice>, String> {
    let Some(kind) = args.first().map(|k| k.to_ascii_lowercase()) else {
        return Ok(vec![
            sample("success", "Build finished", "3 targets"),
            sample("fail", "Compile failed", "main.go:12"),
            sample("published", "Published", "workshop item is live"),
        ]);
    };
    let (mut title, mut body) = match kind.as_str() {
        "success" | "info" | "system" => ("Build finished".to_string(), "3 targets".to_string()),
        "fail" | "error" => ("Compile failed".into(), "main.go:12".into()),
        "published" | "warn" | "warning" | "question" => ("Published".into(), "workshop item is live".into()),
        _ => return Err(format!("unknown notification {:?}", args[0])),
    };
    if let Some(t) = args.get(1) {
        title = t.clone();
    }
    if args.len() > 2 {
        body = args[2..].join(" ");
    }
    Ok(vec![sample(&kind, &title, &body)])
}

/// Runs the CLI; returns the process exit code.
pub fn run(args: &[String], ctx: tauri::Context) -> i32 {
    if let Some(rest) = args.first().filter(|a| matches!(a.as_str(), "show" | "toast")).map(|_| &args[1..]) {
        if rest.first().is_some_and(|k| k == "update") {
            return show_update(rest.get(1).map(String::as_str).unwrap_or(""), ctx);
        }
        return match show_args(rest) {
            Ok(notes) => show(notes, ctx),
            Err(e) => {
                eprintln!("suzuri notify: {e}");
                eprint!("{USAGE}");
                2
            }
        };
    }
    let names = match sound_args(args) {
        Ok(n) => n,
        Err(e) => {
            if e != "help" {
                eprintln!("suzuri notify: {e}");
            }
            eprint!("{USAGE}");
            return 2;
        }
    };
    for (i, name) in names.iter().enumerate() {
        println!("{name}");
        let wav = match *name {
            "fail" => FAIL,
            "published" => PUBLISHED,
            _ => SUCCESS,
        };
        if let Err(e) = play_sync(wav) {
            eprintln!("suzuri notify: {e}");
            return 1;
        }
        if i + 1 < names.len() {
            std::thread::sleep(Duration::from_millis(350));
        }
    }
    0
}

/// Shows the update offer card (as the app does, but its Install only
/// prints: nothing installs from here), then exits once it is answered
/// or after 2 min.
fn show_update(version: &str, mut ctx: tauri::Context) -> i32 {
    println!("showing the update offer — Install or Skip");
    ctx.config_mut().app.windows.clear();
    let version = version.to_string();
    let app = tauri::Builder::default()
        .setup(move |app| {
            app.manage(crate::notices::Notices::new(app.handle().clone()));
            let handle = app.handle().clone();
            let current = handle.package_info().version.to_string();
            std::thread::spawn(move || {
                toast::post(crate::notices::update_note(&version, &current, || println!("  Install (a sample: nothing installs)")));
                let end = Instant::now() + Duration::from_secs(120);
                std::thread::sleep(Duration::from_millis(500));
                while Instant::now() < end && !toast::card_keys().is_empty() {
                    std::thread::sleep(Duration::from_millis(100));
                }
                std::thread::sleep(Duration::from_millis(300));
                handle.exit(0);
            });
            Ok(())
        })
        .build(ctx);
    match app {
        Ok(app) => {
            app.run(|_, _| {});
            0
        }
        Err(e) => {
            eprintln!("suzuri notify: {e}");
            1
        }
    }
}

/// Shows the cards on the desktop (#288) with no terminal window,
/// then exits once they are gone (or 2 min for a card that never expires).
fn show(notes: Vec<Notice>, mut ctx: tauri::Context) -> i32 {
    println!("showing {} notification(s) — click to dismiss, or wait", notes.len());
    // No windows at all: the cards are rust-toast's (notices.rs). Tauri
    // still runs, for the AppKit main thread the Mac's cards need.
    ctx.config_mut().app.windows.clear();
    let app = tauri::Builder::default()
        .setup(move |app| {
            app.manage(crate::notices::Notices::new(app.handle().clone()));
            let handle = app.handle().clone();
            std::thread::spawn(move || {
                let mut last = Instant::now();
                for n in notes {
                    println!("  {} — {}", n.sound, n.title);
                    let end = Instant::now()
                        + match n.expire_ms {
                            Some(0) => Duration::from_secs(120),
                            Some(ms) => Duration::from_millis(ms as u64),
                            None => Duration::from_millis(3200),
                        };
                    last = last.max(end);
                    let _ = handle.state::<crate::notices::Notices>().post(None, n);
                    // Let each sound start before the next card (Go: 450 ms).
                    std::thread::sleep(Duration::from_millis(450));
                }
                while Instant::now() < last + Duration::from_millis(400) {
                    if handle.state::<crate::notices::Notices>().live_count() == 0 {
                        break;
                    }
                    std::thread::sleep(Duration::from_millis(100));
                }
                handle.exit(0);
            });
            Ok(())
        })
        .build(ctx);
    match app {
        Ok(app) => {
            app.run(|_, _| {});
            0
        }
        Err(e) => {
            eprintln!("suzuri notify: {e}");
            1
        }
    }
}

#[cfg(target_os = "macos")]
fn play_sync(wav: &[u8]) -> Result<(), String> {
    // afplay is on every Mac; Go uses AppKit's NSSound for the same files.
    let path = std::env::temp_dir().join(format!("rsz-notify-{}.wav", std::process::id()));
    std::fs::write(&path, wav).map_err(|e| e.to_string())?;
    let r = std::process::Command::new("/usr/bin/afplay").arg(&path).status();
    let _ = std::fs::remove_file(&path);
    match r {
        Ok(s) if s.success() => Ok(()),
        Ok(s) => Err(format!("afplay: {s}")),
        Err(e) => Err(format!("afplay: {e}")),
    }
}

#[cfg(windows)]
fn play_sync(wav: &[u8]) -> Result<(), String> {
    const SND_SYNC: u32 = 0x0000;
    const SND_MEMORY: u32 = 0x0004;
    const SND_NODEFAULT: u32 = 0x0002;
    #[link(name = "winmm")]
    unsafe extern "system" {
        fn PlaySoundW(sound: *const u8, module: *mut std::ffi::c_void, flags: u32) -> i32;
    }
    // SND_MEMORY: the pointer is the WAV image itself.
    if unsafe { PlaySoundW(wav.as_ptr(), std::ptr::null_mut(), SND_MEMORY | SND_SYNC | SND_NODEFAULT) } == 0 {
        return Err("PlaySound failed".into());
    }
    Ok(())
}

#[cfg(not(any(target_os = "macos", windows)))]
fn play_sync(_wav: &[u8]) -> Result<(), String> {
    Err("sounds are macOS and Windows only".into())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn s(v: &[&str]) -> Vec<String> {
        v.iter().map(|x| x.to_string()).collect()
    }

    #[test]
    fn wants_notify_and_sound() {
        assert!(wants(&s(&["rsz", "notify"])));
        assert!(wants(&s(&["rsz", "sound", "fail"])));
        assert!(!wants(&s(&["rsz"])));
        assert!(!wants(&s(&["rsz", "--x"])));
    }

    #[test]
    fn sound_args_match_go() {
        assert_eq!(sound_args(&[]).unwrap(), vec!["success", "fail", "published"]);
        assert_eq!(sound_args(&s(&["error", "Warn", "system"])).unwrap(), vec!["fail", "published", "success"]);
        assert_eq!(sound_args(&s(&["all"])).unwrap().len(), 3);
        assert_eq!(sound_args(&s(&["--help"])).unwrap_err(), "help");
        assert!(sound_args(&s(&["boop"])).unwrap_err().contains("unknown sound"));
    }

    #[test]
    fn show_args_match_go() {
        let three = show_args(&[]).unwrap();
        assert_eq!(three.iter().map(|n| n.sound.as_str()).collect::<Vec<_>>(), vec!["info", "error", "question"]);
        assert_eq!(three[1].urgency, 2);
        assert_eq!(three[1].icon, "error");
        assert!(three.iter().all(|n| n.expire_ms == Some(4000)));
        let one = show_args(&s(&["fail", "Compile failed", "main.go:12", "and", "more"])).unwrap();
        assert_eq!((one[0].title.as_str(), one[0].body.as_str()), ("Compile failed", "main.go:12 and more"));
        // "update" is the action card (notices::update_note), not a Notice.
        assert!(show_args(&s(&["update"])).is_err());
        assert!(show_args(&s(&["nope"])).is_err());
    }

    #[test]
    fn embedded_sounds_are_wavs() {
        for w in [SUCCESS, FAIL, PUBLISHED] {
            assert_eq!(&w[..4], b"RIFF");
            assert_eq!(&w[8..12], b"WAVE");
        }
    }
}
