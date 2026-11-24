//! What both halves send: the JSON for the host's events and the viewer's
//! calls, written and read here (the host's half and the app's viewer's).
//! Keep both directions in step and keep reading what older hosts send.

use base64::Engine as _;
use rsz_term::images::{ImageData, Placement};
use rsz_term::palette::{Palette, Rgb};
use rsz_term::pane::ShellStatus;
use rsz_term::protocols::Notice;
use rsz_term::{MouseEvent, MouseKind, Out, SelectKind, Submit};
use serde_json::{Value, json};

pub const B64: base64::engine::GeneralPurpose = base64::engine::general_purpose::STANDARD;

pub fn rgb(c: Rgb) -> Value {
    json!([c.0, c.1, c.2])
}

pub fn rgb_of(v: &Value) -> Option<Rgb> {
    let a = v.as_array()?;
    Some((
        a.first()?.as_u64()? as u8,
        a.get(1)?.as_u64()? as u8,
        a.get(2)?.as_u64()? as u8,
    ))
}

pub fn palette_json(p: &Palette) -> Value {
    json!({ "t": "palette", "dark": p.dark, "bg": rgb(p.bg), "fg": rgb(p.fg), "panel": rgb(p.panel), "accent": rgb(p.accent),
            "ansi16": p.ansi16.iter().map(|c| rgb(*c)).collect::<Vec<_>>() })
}

pub fn palette_of(v: &Value) -> Option<Palette> {
    let a = v["ansi16"].as_array()?;
    let mut ansi16 = [(0, 0, 0); 16];
    for (i, c) in a.iter().take(16).enumerate() {
        ansi16[i] = rgb_of(c)?;
    }
    Some(Palette {
        dark: v["dark"].as_bool()?,
        bg: rgb_of(&v["bg"])?,
        fg: rgb_of(&v["fg"])?,
        panel: rgb_of(&v["panel"])?,
        accent: rgb_of(&v["accent"])?,
        ansi16,
    })
}

pub fn shell_json(st: &ShellStatus) -> Value {
    json!({ "t": "shell", "integrated": st.integrated, "cwd": st.cwd, "program": st.program, "prompts": st.prompts, "pending": st.pending })
}

pub fn image_json(d: &ImageData) -> Value {
    json!({ "t": "img", "id": d.id, "name": d.name, "path": d.path, "format": d.format, "w": d.width, "h": d.height, "z": d.compressed, "data": B64.encode(&d.data) })
}

pub fn placements_json(list: &[Placement]) -> Value {
    let list: Vec<Value> = list
        .iter()
        .map(|p| {
            json!([
                p.image, p.row, p.col, p.cols, p.rows, p.z, p.inline, p.crop.0, p.crop.1, p.crop.2,
                p.crop.3
            ])
        })
        .collect();
    json!({ "t": "imgpos", "list": list })
}

pub fn notice_json(n: &Notice) -> Value {
    json!({ "t": "notice", "id": n.id, "title": n.title, "body": n.body, "sound": n.sound, "icon": n.icon, "urgency": n.urgency,
            "occasion": n.occasion, "expire_ms": n.expire_ms, "report": n.report, "focus": n.focus, "close_event": n.close_event })
}

/// A host event back into the pane's Out (the viewer's half).
pub fn out_of(v: &Value) -> Option<Out> {
    let s = |k: &str| v[k].as_str().unwrap_or("").to_string();
    let u = |k: &str| v[k].as_u64().unwrap_or(0);
    let b = |k: &str| v[k].as_bool().unwrap_or(false);
    Some(match v["t"].as_str()? {
        "title" => Out::Title(s("title")),
        "bell" => Out::Bell,
        "clip" => Out::Clipboard(s("text")),
        "shell" => Out::Shell(ShellStatus {
            integrated: b("integrated"),
            cwd: s("cwd"),
            program: b("program"),
            prompts: u("prompts"),
            pending: b("pending"),
        }),
        "done" => Out::CommandDone {
            exit: v["exit"].as_i64().map(|x| x as i32),
            command: s("command"),
        },
        "typeahead" => Out::Typeahead(s("text")),
        "exit" => Out::Exit(v["code"].as_u64().map(|x| x as u32)),
        "notice" => Out::Notice(Notice {
            id: s("id"),
            title: s("title"),
            body: s("body"),
            sound: s("sound"),
            icon: s("icon"),
            urgency: u("urgency") as u8,
            occasion: s("occasion"),
            expire_ms: v["expire_ms"].as_u64().map(|x| x as u32),
            report: b("report"),
            focus: b("focus"),
            close_event: b("close_event"),
        }),
        "notice_close" => Out::NoticeClose(s("id")),
        "notice_alive" => Out::NoticeAlive(s("id")),
        "progress" => Out::Progress {
            state: u("state") as u8,
            percent: u("percent") as u8,
        },
        "img" => Out::ImageData(ImageData {
            id: u("id") as u32,
            name: s("name"),
            path: v["path"].as_str().map(str::to_string),
            format: u("format") as u32,
            width: u("w") as u32,
            height: u("h") as u32,
            compressed: b("z"),
            data: B64.decode(v["data"].as_str().unwrap_or("")).ok()?,
        }),
        "imgdrop" => Out::ImageDrop(
            v["ids"]
                .as_array()?
                .iter()
                .filter_map(|x| x.as_u64().map(|x| x as u32))
                .collect(),
        ),
        "imgpos" => Out::Placements(
            v["list"]
                .as_array()?
                .iter()
                .filter_map(|p| {
                    let a = p.as_array()?;
                    let n = |i: usize| a.get(i).and_then(|x| x.as_i64()).unwrap_or(0);
                    Some(Placement {
                        image: n(0) as u32,
                        row: n(1) as i32,
                        col: n(2) as u16,
                        cols: n(3) as u32,
                        rows: n(4) as u32,
                        z: n(5) as i32,
                        inline: a.get(6).and_then(|x| x.as_bool()).unwrap_or(false),
                        crop: (n(7) as u32, n(8) as u32, n(9) as u32, n(10) as u32),
                    })
                })
                .collect(),
        ),
        _ => return None,
    })
}

pub fn mouse_json(ev: &MouseEvent) -> Value {
    let (kind, held) = match ev.kind {
        MouseKind::Press => ("press", None),
        MouseKind::Release => ("release", None),
        MouseKind::Motion(b) => ("motion", b),
        MouseKind::WheelUp => ("wheel_up", None),
        MouseKind::WheelDown => ("wheel_down", None),
    };
    json!({ "t": "mouse", "kind": kind, "held": held, "button": ev.button, "col": ev.col, "row": ev.row, "shift": ev.shift, "alt": ev.alt, "ctrl": ev.ctrl })
}

pub fn mouse_of(v: &Value) -> Option<MouseEvent> {
    let kind = match v["kind"].as_str()? {
        "press" => MouseKind::Press,
        "release" => MouseKind::Release,
        "motion" => MouseKind::Motion(v["held"].as_u64().map(|b| b as u8)),
        "wheel_up" => MouseKind::WheelUp,
        "wheel_down" => MouseKind::WheelDown,
        _ => return None,
    };
    let b = |k: &str| v[k].as_bool().unwrap_or(false);
    Some(MouseEvent {
        kind,
        button: v["button"].as_u64()? as u8,
        col: v["col"].as_u64()? as u16,
        row: v["row"].as_u64()? as u16,
        shift: b("shift"),
        alt: b("alt"),
        ctrl: b("ctrl"),
    })
}

pub fn select_kind(s: &str) -> SelectKind {
    match s {
        "word" => SelectKind::Word,
        "line" => SelectKind::Line,
        _ => SelectKind::Cell,
    }
}

pub fn select_kind_name(k: SelectKind) -> &'static str {
    match k {
        SelectKind::Cell => "cell",
        SelectKind::Word => "word",
        SelectKind::Line => "line",
    }
}

pub fn submit_json(s: Submit) -> Value {
    match s {
        Submit::Blank => json!({ "kind": "blank" }),
        Submit::Sent => json!({ "kind": "sent" }),
        Submit::Queued(n) => json!({ "kind": "queued", "n": n }),
        Submit::Busy => json!({ "kind": "busy" }),
    }
}

pub fn submit_of(v: &Value) -> Submit {
    match v["kind"].as_str() {
        Some("blank") => Submit::Blank,
        Some("queued") => Submit::Queued(v["n"].as_u64().unwrap_or(0) as usize),
        Some("busy") => Submit::Busy,
        _ => Submit::Sent,
    }
}
