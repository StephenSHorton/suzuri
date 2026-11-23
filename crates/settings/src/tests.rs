use super::*;

fn tmp(name: &str) -> PathBuf {
    let d = std::env::temp_dir().join(format!("rsz-settings-{name}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&d);
    std::fs::create_dir_all(&d).unwrap();
    d
}

fn on_disk(p: &Path) -> Value {
    serde_json::from_slice(&std::fs::read(p).unwrap()).unwrap()
}

// ---- Reading.

#[test]
fn defaults_when_nothing_exists() {
    let d = tmp("defaults");
    let v = load_from(&d.join("rs/config.json"), &d.join("go/config.json"));
    assert_eq!(v["font_size_px"], 14);
    assert_eq!(v["font_face"], FONT_FACE_DEFAULT);
    assert_eq!(v["cursor"], "block");
    assert_eq!(v["ui_theme"], "neutral");
    assert_eq!(v["ui_mode"], "dark");
    assert!(v.get("theme").is_none());
    assert_eq!(v["active_profile"], "Default");
    assert!(v["profiles"].as_array().unwrap().len() >= 3);
}

#[test]
fn reads_a_file_and_keeps_unknown_keys() {
    let d = tmp("read");
    let p = d.join("config.json");
    std::fs::write(&p, r#"{"font_face": " Menlo ", "font_size_px": 5, "cursor": "BAR", "intro": "matrix", "glass_blur": 48}"#).unwrap();
    let v = Store::at(&p).load();
    assert_eq!(v["font_face"], "Menlo");
    assert_eq!(v["font_size_px"], FONT_SIZE_MIN, "clamped");
    assert_eq!(v["cursor"], "bar");
    assert_eq!(v["intro"], "matrix");
    assert_eq!(v["glass_blur"], 48);
}

#[test]
fn imports_go_suzuri_once_and_keeps_unknown_keys() {
    let d = tmp("import");
    let go = d.join("go/config.json");
    std::fs::create_dir_all(go.parent().unwrap()).unwrap();
    std::fs::write(
        &go,
        r#"{"font_size_px": 16, "cursor": "bar", "theme": "Nord", "intro": "matrix",
            "glass_blur": 48, "profiles": [{"name": "Default"}, {"name": "Work", "cwd": "/tmp"}],
            "active_profile": "work", "window": {"x": 0, "y": 65, "width": 1728, "height": 1052}}"#,
    )
    .unwrap();
    let rs = d.join("rs/config.json");
    let v = load_from(&rs, &go);
    assert_eq!(v["font_size_px"], 16);
    assert_eq!(v["cursor"], "bar");
    // Go's terminal theme doesn't carry over: the default look does.
    assert!(v.get("theme").is_none());
    assert_eq!(v["ui_theme"], "neutral");
    assert_eq!(v["active_profile"], "Work");
    assert_eq!(v["intro"], "matrix");
    assert_eq!(v["window"]["width"], 1728);
    assert!(rs.exists(), "the import is saved as our own file");
    // Later changes to Go's file don't leak in: ours exists now.
    std::fs::write(&go, r#"{"font_size_px": 20}"#).unwrap();
    assert_eq!(load_from(&rs, &go)["font_size_px"], 16);
}

#[test]
fn broken_file_gives_defaults_and_is_not_replaced_by_import() {
    let d = tmp("broken");
    let (rs, go) = (d.join("rs/config.json"), d.join("go/config.json"));
    std::fs::create_dir_all(rs.parent().unwrap()).unwrap();
    std::fs::write(&rs, "{ not json").unwrap();
    std::fs::create_dir_all(go.parent().unwrap()).unwrap();
    std::fs::write(&go, r#"{"font_size_px": 20}"#).unwrap();
    assert_eq!(load_from(&rs, &go)["font_size_px"], 14);
    assert_eq!(std::fs::read_to_string(&rs).unwrap(), "{ not json");
}

// ---- Saving.

#[test]
fn update_merges_clamps_and_persists() {
    let d = tmp("update");
    let (rs, go) = (d.join("rs/config.json"), d.join("go/config.json"));
    let v = update_at(&rs, &go, json!({"font_size_px": 99, "ui_theme": "nope", "extra": 1})).unwrap();
    assert_eq!(v["font_size_px"], 36);
    assert_eq!(v["ui_theme"], "neutral");
    assert_eq!(v["extra"], 1);
    let v = update_at(&rs, &go, json!({"cursor": "underline"})).unwrap();
    assert_eq!(v["extra"], 1, "earlier keys kept");
    assert_eq!(load_from(&rs, &go)["cursor"], "underline");
    // What is written is normalized too.
    assert_eq!(on_disk(&rs)["font_size_px"], 36);
}

#[test]
fn saving_keeps_unknown_keys_and_values_it_does_not_know() {
    // A newer program's settings: a key this crate has never heard of, and
    // a value of a known key it doesn't know (a theme added later).
    let d = tmp("unknown");
    let p = d.join("config.json");
    std::fs::write(&p, r#"{"ui_theme": "aurora", "future_key": {"a": [1, 2]}, "intro": "matrix", "theme": "Nord"}"#).unwrap();
    let s = Store::at(&p);
    assert_eq!(s.load()["ui_theme"], "neutral", "read as the default here");
    let v = s.update(json!({"font_size_px": 18})).unwrap();
    assert_eq!(v["font_size_px"], 18);
    let disk = on_disk(&p);
    assert_eq!(disk["font_size_px"], 18);
    assert_eq!(disk["ui_theme"], "aurora", "an unpatched value is never reset");
    assert_eq!(disk["future_key"], json!({"a": [1, 2]}));
    assert_eq!(disk["intro"], "matrix");
    assert!(disk.get("theme").is_none(), "retired keys go");
}

#[test]
fn profiles_and_active_profile_save_together() {
    let d = tmp("profiles");
    let p = d.join("config.json");
    std::fs::write(&p, r#"{"profiles": [{"name": "Dev"}, {"name": "Ops"}], "active_profile": "Ops"}"#).unwrap();
    let v = Store::at(&p).update(json!({"profiles": [{"name": "Dev"}]})).unwrap();
    assert_eq!(v["active_profile"], "Dev");
    assert_eq!(on_disk(&p)["active_profile"], "Dev", "the file never names a profile that is gone");
}

#[test]
fn writers_at_once_never_lose_the_file_or_a_key() {
    // Each thread stands in for a program: its own file lock, then read,
    // merge and write a key of its own.
    let d = tmp("race");
    let (rs, go) = (d.join("rs/config.json"), d.join("go/config.json"));
    let hs: Vec<_> = (0..8)
        .map(|i| {
            let (rs, go) = (rs.clone(), go.clone());
            std::thread::spawn(move || {
                for j in 0..10 {
                    let _f = lock_file(&rs).expect("lock");
                    update_at(&rs, &go, json!({format!("k{i}"): j})).unwrap();
                }
            })
        })
        .collect();
    for h in hs {
        h.join().unwrap();
    }
    let v = load_from(&rs, &go);
    for i in 0..8 {
        assert_eq!(v[format!("k{i}")], 9);
    }
    let left: Vec<_> = std::fs::read_dir(rs.parent().unwrap())
        .unwrap()
        .filter_map(|e| e.ok()?.file_name().into_string().ok())
        .filter(|n| n.ends_with(".tmp"))
        .collect();
    assert!(left.is_empty(), "temp files left: {left:?}");
}

// ---- Changes seen by another reader.

const FAST: Duration = Duration::from_millis(20);
const WAIT: Duration = Duration::from_secs(5);

fn watching(s: &Store) -> (Watch, mpsc::Receiver<Value>) {
    let (tx, rx) = mpsc::channel();
    let w = s.watch_every(FAST, move |v| {
        let _ = tx.send(v);
    });
    (w, rx)
}

#[test]
fn a_change_is_seen_by_a_second_reader() {
    let d = tmp("watch");
    let p = d.join("config.json");
    let (one, two) = (Store::at(&p), Store::at(&p));
    assert_eq!(two.load()["font_size_px"], 14);
    let (_w, rx) = watching(&two);
    one.update(json!({"font_size_px": 20, "cursor": "bar"})).unwrap();
    let v = rx.recv_timeout(WAIT).expect("the second reader is told");
    assert_eq!(v["font_size_px"], 20);
    assert_eq!(v["cursor"], "bar");
    assert_eq!(two.load()["font_size_px"], 20);
    // And again, for the next change.
    one.update(json!({"ui_theme": "matcha"})).unwrap();
    assert_eq!(rx.recv_timeout(WAIT).unwrap()["ui_theme"], "matcha");
}

#[test]
fn a_programs_own_saves_do_not_come_back_to_it() {
    let d = tmp("own");
    let p = d.join("config.json");
    let s = Store::at(&p);
    let (_w, rx) = watching(&s);
    s.update(json!({"font_size_px": 22})).unwrap();
    s.update(json!({"cursor": "underline"})).unwrap();
    assert!(rx.recv_timeout(Duration::from_millis(300)).is_err(), "no callback for our own saves");
    // A hand edit does.
    std::fs::write(&p, r#"{"font_size_px": 12}"#).unwrap();
    assert_eq!(rx.recv_timeout(WAIT).unwrap()["font_size_px"], 12);
}

#[test]
fn a_broken_file_is_skipped_not_taken_as_the_defaults() {
    let d = tmp("watch-broken");
    let p = d.join("config.json");
    let s = Store::at(&p);
    s.update(json!({"font_size_px": 22})).unwrap();
    let (_w, rx) = watching(&s);
    std::fs::write(&p, r#"{"font_size_px": 2"#).unwrap();
    assert!(rx.recv_timeout(Duration::from_millis(300)).is_err(), "half a file is not a change");
    std::fs::write(&p, r#"{"font_size_px": 24}"#).unwrap();
    assert_eq!(rx.recv_timeout(WAIT).unwrap()["font_size_px"], 24);
}

#[test]
fn dropping_the_watch_stops_it() {
    let d = tmp("watch-stop");
    let p = d.join("config.json");
    let (one, two) = (Store::at(&p), Store::at(&p));
    let (w, rx) = watching(&two);
    drop(w);
    one.update(json!({"font_size_px": 30})).unwrap();
    assert!(rx.recv_timeout(Duration::from_millis(300)).is_err());
}

// ---- Normalizing.

#[test]
fn ui_theme_and_mode_are_astryx_ones_and_the_old_theme_keys_go() {
    let v = normalize(json!({"theme": "dracula", "shell_ansi_map": "full", "intro": "matrix"}));
    assert!(v.get("theme").is_none());
    assert!(v.get("shell_ansi_map").is_none());
    assert_eq!(v["intro"], "matrix", "unknown keys stay");
    assert_eq!((v["ui_theme"].as_str(), v["ui_mode"].as_str()), (Some("neutral"), Some("dark")));
    let v = normalize(json!({"ui_theme": " Y2K ", "ui_mode": "System"}));
    assert_eq!((v["ui_theme"].as_str(), v["ui_mode"].as_str()), (Some("y2k"), Some("system")));
    let v = normalize(json!({"ui_theme": "dracula", "ui_mode": "dim"}));
    assert_eq!((v["ui_theme"].as_str(), v["ui_mode"].as_str()), (Some("neutral"), Some("dark")));
    assert_eq!(UI_THEMES.len(), UI_THEME_LABELS.len());
    assert_eq!(UI_MODES.len(), UI_MODE_LABELS.len());
}

#[test]
fn ui_text_size_is_a_category() {
    assert_eq!(normalize(json!({}))["ui_text_size"], TEXT_SIZE_DEFAULT);
    assert_eq!(normalize(json!({"ui_text_size": " XXL "}))["ui_text_size"], "xxl");
    assert_eq!(normalize(json!({"ui_text_size": "huge"}))["ui_text_size"], TEXT_SIZE_DEFAULT);
    assert_eq!(TEXT_SIZES.len(), TEXT_SIZE_LABELS.len());
}

#[test]
fn notice_position_is_one_of_go_anchors() {
    assert_eq!(normalize(json!({}))["notice_position"], "bottom-left");
    assert_eq!(normalize(json!({"notice_position": " Top-Right "}))["notice_position"], "top-right");
    assert_eq!(normalize(json!({"notice_position": "upstairs"}))["notice_position"], "bottom-left");
    // Go's other anchors move to the nearest corner.
    for (from, to) in [
        ("bottom-center", "bottom-right"),
        ("center-left", "bottom-left"),
        ("center", "bottom-right"),
        ("center-right", "bottom-right"),
        ("top-center", "top-right"),
        ("top-left", "top-left"),
        ("bottom-right", "bottom-right"),
    ] {
        assert_eq!(normalize(json!({"notice_position": from}))["notice_position"], to, "{from}");
    }
    for p in NOTICE_POSITIONS {
        assert_eq!(notice_corner(p), *p);
    }
}

#[test]
fn animate_unfocused_and_restore_sessions_default_on() {
    for k in ["animate_unfocused", "restore_sessions"] {
        assert_eq!(normalize(json!({}))[k], true);
        assert_eq!(normalize(json!({k: false}))[k], false);
        assert_eq!(normalize(json!({k: "nope"}))[k], true);
    }
}

#[test]
fn profiles_are_unique_and_launch() {
    let v = normalize(json!({
        "profiles": [{"name": " Dev ", "shell": "\"/bin/my shell\" -l", "cwd": "~/src"}, {"name": "dev"}, {"name": ""}],
        "active_profile": "missing"
    }));
    let ps = v["profiles"].as_array().unwrap();
    assert_eq!(ps.len(), 1);
    assert_eq!(v["active_profile"], "Dev");
    let (argv, cwd) = profile_launch(&v, None);
    assert_eq!(argv.unwrap(), vec!["/bin/my shell", "-l"]);
    assert!(cwd.unwrap().ends_with("/src"));
    assert_eq!(profile_launch(&v, Some("nothing")), (None, None));
}

#[test]
fn split_args_keeps_quotes() {
    assert_eq!(split_args(r#""C:\Program Files\x.exe" --a "b c""#), vec![r"C:\Program Files\x.exe", "--a", "b c"]);
    assert_eq!(split_args("claude"), vec!["claude"]);
}

#[test]
fn cursor_codes() {
    assert_eq!(cursor_code(&json!({"cursor": "block"})), 1);
    assert_eq!(cursor_code(&json!({"cursor": "underline"})), 2);
    assert_eq!(cursor_code(&json!({"cursor": "bar"})), 3);
}

#[test]
fn meta_lists_every_choice_with_its_label() {
    let m = meta();
    for (ids, labels) in [("ui_themes", "ui_theme_labels"), ("ui_modes", "ui_mode_labels"), ("notice_positions", "notice_labels"), ("text_sizes", "text_size_labels")] {
        for id in m[ids].as_array().unwrap() {
            assert!(m[labels][id.as_str().unwrap()].is_string(), "{ids}: {id}");
        }
    }
    assert_eq!(m["font_min"], FONT_SIZE_MIN);
    assert_eq!(m["fonts"][0], FONT_FACE_DEFAULT);
}
