//! Terminal protocols the VT engine doesn't implement, taken out of the PTY
//! byte stream before the engine sees it (Go suzuri's termModes.feed,
//! stripAndTakeImages and feedKittyAPCs, card #288):
//!
//! - notices: OSC 9 (iTerm2; `9;4;…` is ConEmu progress), OSC 777
//!   (`777;notify;title;body`), OSC 99 (kitty's notification protocol)
//! - inline images: OSC 1337 `File=…:<base64>` (iTerm2), OSC 7879
//!   `image=<path>` (suzuri's own), and image paths printed as text
//! - Kitty graphics: APC `ESC _ G <key=value,…> ; <payload> ESC \`
//!
//! Everything else passes through untouched, in order. A sequence split
//! across reads is held until it completes: half a Kitty APC reaching the
//! grid prints its base64 as text. Events come out at the point in the
//! stream where their sequence was, after the text before it, so a
//! placement sees the right cursor.

use base64::Engine as _;
use base64::engine::general_purpose::{STANDARD, STANDARD_NO_PAD};

/// Go suzuri's decode cap for one image (maxImageFileBytes).
pub const MAX_IMAGE_BYTES: usize = 12 << 20;
/// A held sequence may grow to the base64 of one image plus headroom.
const MAX_HELD: usize = MAX_IMAGE_BYTES * 4 / 3 + 4096;

/// One notification a program asked for (Go's deskNote).
#[derive(Debug, Clone, PartialEq)]
pub struct Notice {
    /// OSC 99's identifier: a later notice with the same id replaces it.
    pub id: String,
    pub title: String,
    pub body: String,
    /// system, silent, error, warn, warning, info, question
    pub sound: String,
    /// error, warn, warning, info, question, help, or empty
    pub icon: String,
    /// 0 low, 1 normal, 2 critical
    pub urgency: u8,
    /// always, unfocused, invisible
    pub occasion: String,
    /// Some(0) never expires; None takes the default (3.2 s, 6.4 s critical).
    pub expire_ms: Option<u32>,
    /// Click reports back to the program (OSC 99 a=report).
    pub report: bool,
    /// Click focuses the pane (OSC 99 a=focus, the default).
    pub focus: bool,
    /// Closing sends the program a close event (OSC 99 c=1).
    pub close_event: bool,
}

/// Kitty graphics commands Go suzuri handles (a=t, a=T, a=p, a=d).
#[derive(Debug, Clone, PartialEq)]
pub enum Kitty {
    /// An image arrived. `data` is the decoded payload: PNG/JPEG/GIF for
    /// f=100, raw pixels for f=24 (RGB) and f=32 (RGBA) at `width` x
    /// `height`, zlib-compressed when `compressed` (o=z). `place` is set for
    /// a=T: show it at the cursor right away.
    Image { id: u32, format: u32, width: u32, height: u32, compressed: bool, data: Vec<u8>, place: Option<KittyPlace> },
    /// a=p: show image `id` at the cursor.
    Place { id: u32, place: KittyPlace },
    /// a=d: delete one image and its placements, or all (`id` None).
    Delete { id: Option<u32> },
}

#[derive(Debug, Clone, Copy, PartialEq, Default)]
pub struct KittyPlace {
    /// Size in cells (Go: 10 x 5 for a=T, 1 x 1 for a=p when not given).
    pub cols: u32,
    pub rows: u32,
    pub z: i32,
    /// Source crop in pixels; w/h 0 is the whole image.
    pub x: u32,
    pub y: u32,
    pub w: u32,
    pub h: u32,
}

#[derive(Debug, Clone, PartialEq)]
pub enum ProtoEvent {
    Notice(Notice),
    /// OSC 99 p=close: close the live notice with this id.
    CloseNotice(String),
    /// OSC 99 p=alive: the program asks which of its notices are still up;
    /// answer with [`alive_reply`].
    AliveQuery(String),
    /// Bytes to write back to the program.
    Reply(Vec<u8>),
    /// ConEmu OSC 9;4: state 0 none, 1 normal, 2 error, 3 indeterminate,
    /// 4 paused; percent 0-100.
    Progress { state: u8, percent: u8 },
    /// An inline image's file bytes (OSC 1337) and its name.
    InlineImage { name: String, data: Vec<u8> },
    /// An image path to load (OSC 7879, or printed as text), unresolved.
    ImagePath(String),
    Kitty(Kitty),
}

/// Where [`Protocols::feed`] sends its output, in stream order.
pub trait ProtoSink {
    fn text(&mut self, bytes: &[u8]);
    fn event(&mut self, ev: ProtoEvent);
}

#[derive(Default)]
pub struct Protocols {
    held: Vec<u8>,
    /// Swallowing an oversized sequence until its terminator.
    skipping: bool,
    osc99: Vec<(String, Osc99Buf)>,
    kitty_open: Option<KittyOpen>,
    /// Scan printed text for image paths (Go's findImagePathsInText). Off by
    /// default: Go suzuri 0.9.167 shows nothing for them on the Mac, and on
    /// it every `ls` or `find` that prints image paths would add images.
    pub scan_paths: bool,
}

struct KittyOpen {
    id: u32,
    meta: KittyMeta,
    buf: Vec<u8>,
}

#[derive(Clone, Copy)]
struct KittyMeta {
    format: u32,
    width: u32,
    height: u32,
    compressed: bool,
    cols: u32,
    rows: u32,
    z: i32,
    place: bool,
}

enum Seq {
    Osc,
    Apc,
}

impl Protocols {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn feed(&mut self, input: &[u8], sink: &mut impl ProtoSink) {
        let joined;
        let data: &[u8] = if self.held.is_empty() {
            input
        } else {
            let mut v = std::mem::take(&mut self.held);
            v.extend_from_slice(input);
            joined = v;
            &joined
        };
        let mut i = 0;
        if self.skipping {
            match find_st(data, 0) {
                Some((_, end)) => {
                    self.skipping = false;
                    i = end;
                }
                None => {
                    // Keep a trailing ESC: it may be the start of the ST.
                    if data.last() == Some(&0x1b) {
                        self.held.push(0x1b);
                    }
                    return;
                }
            }
        }
        let mut text_start = i;
        let mut paths = Vec::new();
        while i < data.len() {
            if data[i] != 0x1b {
                i += 1;
                continue;
            }
            let Some(&next) = data.get(i + 1) else {
                self.flush_text(&data[text_start..i], &mut paths, sink);
                self.held.push(0x1b);
                text_start = data.len();
                break;
            };
            let kind = match next {
                b']' => Seq::Osc,
                b'_' => Seq::Apc,
                _ => {
                    i += 2;
                    continue;
                }
            };
            let body_start = i + 2;
            if matches!(kind, Seq::Apc) {
                match data.get(body_start) {
                    None => {
                        self.flush_text(&data[text_start..i], &mut paths, sink);
                        self.hold(&data[i..]);
                        text_start = data.len();
                        break;
                    }
                    Some(b'G') => {}
                    // Other APCs: the engine ignores them itself.
                    Some(_) => {
                        i += 2;
                        continue;
                    }
                }
            }
            match find_st(data, body_start) {
                Some((body_end, end)) => {
                    let body = &data[body_start..body_end];
                    let ours = match kind {
                        Seq::Osc => osc_is_ours(body),
                        Seq::Apc => true,
                    };
                    if ours {
                        self.flush_text(&data[text_start..i], &mut paths, sink);
                        match kind {
                            Seq::Osc => self.take_osc(body, sink),
                            Seq::Apc => self.take_kitty(&body[1..], sink),
                        }
                        text_start = end;
                    }
                    i = end;
                }
                None => {
                    let partial = &data[body_start..];
                    let maybe_ours = match kind {
                        Seq::Osc => osc_may_be_ours(partial),
                        Seq::Apc => true,
                    };
                    if maybe_ours {
                        self.flush_text(&data[text_start..i], &mut paths, sink);
                        if data.len() - i > MAX_HELD {
                            // A runaway image: drop it, and its tail when it comes.
                            self.skipping = true;
                            self.kitty_open = None;
                        } else {
                            self.hold(&data[i..]);
                        }
                        text_start = data.len();
                    }
                    // Not ours: the engine's own parser carries it across reads.
                    break;
                }
            }
        }
        if text_start < data.len() {
            self.flush_text(&data[text_start..], &mut paths, sink);
        }
        for p in dedup(paths) {
            sink.event(ProtoEvent::ImagePath(p));
        }
    }

    fn hold(&mut self, b: &[u8]) {
        self.held.clear();
        self.held.extend_from_slice(b);
    }

    fn flush_text(&self, b: &[u8], paths: &mut Vec<String>, sink: &mut impl ProtoSink) {
        if b.is_empty() {
            return;
        }
        if self.scan_paths {
            paths.extend(find_image_paths(&String::from_utf8_lossy(b)));
        }
        sink.text(b);
    }

    fn take_osc(&mut self, body: &[u8], sink: &mut impl ProtoSink) {
        let s = String::from_utf8_lossy(body);
        if let Some(rest) = s.strip_prefix("1337;File=") {
            if let Some((name, data)) = parse_iterm_file(rest) {
                sink.event(ProtoEvent::InlineImage { name, data });
            }
        } else if let Some(p) = s.strip_prefix("7879;image=") {
            let p = p.trim();
            if !p.is_empty() {
                sink.event(ProtoEvent::ImagePath(p.to_string()));
            }
        } else if s == "99" || s.starts_with("99;") {
            self.take_osc99(s.strip_prefix("99;").unwrap_or(""), sink);
        } else if let Some(rest) = s.strip_prefix("777;") {
            take_osc777(rest, sink);
        } else if let Some(rest) = s.strip_prefix("9;") {
            take_osc9(rest, sink);
        }
    }

    // OSC 99 (Go takeOSC99): chunks with the same id build one notice until
    // d=0 stops arriving.
    fn take_osc99(&mut self, rest: &str, sink: &mut impl ProtoSink) {
        let (meta, body) = rest.split_once(';').unwrap_or((rest, ""));
        let kv = parse_osc99_meta(meta);
        let id = sanitize_notice_id(last(&kv, 'i').unwrap_or(""));
        let done = last(&kv, 'd').is_none_or(|d| d != "0");
        let mut p = last(&kv, 'p').unwrap_or("").to_string();
        if p.is_empty() {
            p = "title".into();
        }
        let mut text = decode_osc99_payload(body, last(&kv, 'e') == Some("1"));
        match p.as_str() {
            "?" => {
                sink.event(ProtoEvent::Reply(osc99_query_reply(&id)));
                return;
            }
            "alive" => {
                sink.event(ProtoEvent::AliveQuery(id));
                return;
            }
            "close" => {
                if !id.is_empty() {
                    sink.event(ProtoEvent::CloseNotice(id));
                }
                return;
            }
            "title" | "body" => {}
            _ => {
                text.clear();
                p.clear();
            }
        }
        let at = match self.osc99.iter().position(|(k, _)| *k == id) {
            Some(at) => at,
            None => {
                self.osc99.push((id.clone(), Osc99Buf::new()));
                self.osc99.len() - 1
            }
        };
        let buf = &mut self.osc99[at].1;
        if !id.is_empty() {
            buf.id = id.clone();
        }
        buf.apply(&kv);
        match p.as_str() {
            "title" => buf.title.push_str(&text),
            "body" => buf.body.push_str(&text),
            _ => {}
        }
        if !done {
            return;
        }
        let (_, buf) = self.osc99.remove(at);
        let n = buf.into_notice();
        if n.title.trim().is_empty() && n.body.trim().is_empty() {
            return;
        }
        sink.event(ProtoEvent::Notice(n));
    }

    // Kitty graphics (Go handleAPC / finishTransmit). `body` is after the G.
    fn take_kitty(&mut self, body: &[u8], sink: &mut impl ProtoSink) {
        let (header, payload) = match body.iter().position(|&b| b == b';') {
            Some(p) => (&body[..p], &body[p + 1..]),
            None => (body, &[][..]),
        };
        let kv = parse_kitty_kv(&String::from_utf8_lossy(header));
        let get = |k: &str| kv.iter().rev().find(|(kk, _)| kk == k).map(|(_, v)| v.as_str());
        let num = |k: &str, def: i64| get(k).and_then(|v| v.parse::<i64>().ok()).unwrap_or(def);
        let id = num("i", 0).clamp(0, u32::MAX as i64) as u32;
        let more = get("m") == Some("1");
        let action = get("a").unwrap_or("");
        if action.is_empty() {
            // A continuation chunk of the open transmission.
            let Some(open) = self.kitty_open.as_mut() else { return };
            if open.buf.len() + payload.len() > MAX_HELD {
                self.kitty_open = None;
                return;
            }
            open.buf.extend_from_slice(payload);
            if !more {
                let open = self.kitty_open.take().unwrap();
                finish_kitty(open.id, &open.buf, open.meta, sink);
            }
            return;
        }
        match action {
            "t" | "T" => {
                let meta = KittyMeta {
                    format: num("f", 100).max(0) as u32,
                    width: num("s", 0).max(0) as u32,
                    height: num("v", 0).max(0) as u32,
                    compressed: get("o") == Some("z"),
                    cols: num("c", 0).max(0) as u32,
                    rows: num("r", 0).max(0) as u32,
                    z: num("z", 1) as i32,
                    place: action == "T",
                };
                if payload.len() > MAX_HELD {
                    return;
                }
                if more {
                    self.kitty_open = Some(KittyOpen { id, meta, buf: payload.to_vec() });
                } else {
                    self.kitty_open = None;
                    finish_kitty(id, payload, meta, sink);
                }
            }
            "p" => {
                let place = KittyPlace {
                    cols: num("c", 1).max(1) as u32,
                    rows: num("r", 1).max(1) as u32,
                    z: num("z", 1) as i32,
                    x: num("x", 0).max(0) as u32,
                    y: num("y", 0).max(0) as u32,
                    w: num("w", 0).max(0) as u32,
                    h: num("h", 0).max(0) as u32,
                };
                sink.event(ProtoEvent::Kitty(Kitty::Place { id, place }));
            }
            "d" => {
                let all = get("d") == Some("A") || (get("d").unwrap_or("").is_empty() && id == 0);
                sink.event(ProtoEvent::Kitty(Kitty::Delete { id: if all { None } else { Some(id) } }));
            }
            _ => {}
        }
    }
}

fn finish_kitty(id: u32, b64: &[u8], meta: KittyMeta, sink: &mut impl ProtoSink) {
    let Some(data) = decode_b64(b64) else { return };
    if data.is_empty() || data.len() > MAX_IMAGE_BYTES {
        return;
    }
    let place = meta.place.then(|| KittyPlace {
        cols: if meta.cols < 1 { 10 } else { meta.cols },
        rows: if meta.rows < 1 { 5 } else { meta.rows },
        z: meta.z,
        ..Default::default()
    });
    sink.event(ProtoEvent::Kitty(Kitty::Image {
        id,
        format: meta.format,
        width: meta.width,
        height: meta.height,
        compressed: meta.compressed,
        data,
        place,
    }));
}

/// The end of an OSC/APC body starting at `from`: (body end, after the
/// terminator). BEL or ESC \.
fn find_st(data: &[u8], from: usize) -> Option<(usize, usize)> {
    let mut j = from;
    while j < data.len() {
        match data[j] {
            0x07 => return Some((j, j + 1)),
            0x1b if data.get(j + 1) == Some(&b'\\') => return Some((j, j + 2)),
            _ => {}
        }
        j += 1;
    }
    None
}

const OURS: [&[u8]; 5] = [b"9;", b"777;", b"99;", b"1337;File=", b"7879;image="];

fn osc_is_ours(body: &[u8]) -> bool {
    body == b"99" || OURS.iter().any(|p| body.starts_with(p))
}

/// Could this unfinished OSC body still turn out to be one of ours?
fn osc_may_be_ours(partial: &[u8]) -> bool {
    OURS.iter().any(|p| p.starts_with(partial) || partial.starts_with(p))
}

fn take_osc9(rest: &str, sink: &mut impl ProtoSink) {
    if let Some(prog) = rest.strip_prefix("4;") {
        let mut parts = prog.split(';');
        let st = parts.next().and_then(|s| s.parse::<i32>().ok()).unwrap_or(0);
        let pct = parts.next().and_then(|s| s.parse::<i32>().ok()).unwrap_or(0);
        let st = if (0..=4).contains(&st) { st } else { 0 };
        sink.event(ProtoEvent::Progress { state: st as u8, percent: pct.clamp(0, 100) as u8 });
        return;
    }
    let msg = rest.trim();
    if msg.is_empty() {
        return;
    }
    sink.event(ProtoEvent::Notice(simple_notice("Suzuri", clip(msg, 200))));
}

fn take_osc777(rest: &str, sink: &mut impl ProtoSink) {
    let parts: Vec<&str> = rest.splitn(3, ';').collect();
    if parts.first() != Some(&"notify") {
        return;
    }
    let (mut title, mut body) = match parts.len() {
        2 => ("Suzuri", parts[1]),
        3 => (parts[1], parts[2]),
        _ => ("Suzuri", ""),
    };
    body = body.trim();
    title = title.trim();
    if body.is_empty() {
        body = title;
        title = "Suzuri";
    }
    if body.is_empty() {
        return;
    }
    sink.event(ProtoEvent::Notice(simple_notice(&clip(title, 80), clip(body, 200))));
}

fn simple_notice(title: &str, body: String) -> Notice {
    Notice {
        id: String::new(),
        title: title.to_string(),
        body,
        sound: "info".into(),
        icon: String::new(),
        urgency: 1,
        occasion: "always".into(),
        expire_ms: None,
        report: false,
        focus: true,
        close_event: false,
    }
}

struct Osc99Buf {
    id: String,
    title: String,
    body: String,
    sound: String,
    icon: String,
    urgency: u8,
    occasion: String,
    expire_ms: i32,
    report: bool,
    focus: bool,
    close_event: bool,
}

impl Osc99Buf {
    fn new() -> Self {
        Self {
            id: String::new(),
            title: String::new(),
            body: String::new(),
            sound: "system".into(),
            icon: String::new(),
            urgency: 1,
            occasion: "always".into(),
            expire_ms: -1,
            report: false,
            focus: true,
            close_event: false,
        }
    }

    fn apply(&mut self, kv: &[(char, String)]) {
        if let Some(n) = last(kv, 'u').and_then(|u| u.parse::<u8>().ok()).filter(|n| *n <= 2) {
            self.urgency = n;
        }
        if let Some(o) = last(kv, 'o').filter(|o| matches!(*o, "always" | "unfocused" | "invisible")) {
            self.occasion = o.to_string();
        }
        if let Some(s) = last(kv, 's').and_then(|s| named(s, is_sound_name)) {
            self.sound = s;
        }
        if let Some(n) = last(kv, 'w').and_then(|w| w.parse::<i32>().ok()).filter(|n| (-1..600_000).contains(n)) {
            self.expire_ms = n;
        }
        if let Some(c) = last(kv, 'c') {
            self.close_event = c == "1";
        }
        if let Some(a) = last(kv, 'a') {
            self.focus = false;
            self.report = false;
            for part in a.split(',').map(str::trim) {
                match part {
                    "focus" => self.focus = true,
                    "report" => self.report = true,
                    _ => {}
                }
            }
        }
        if let Some(icon) = kv.iter().filter(|(k, _)| *k == 'n').find_map(|(_, v)| named(v, is_icon_name)) {
            self.icon = icon;
        }
    }

    fn into_notice(self) -> Notice {
        Notice {
            id: self.id,
            title: clip(&self.title, 180),
            body: clip(&self.body, 400),
            sound: self.sound,
            icon: self.icon,
            urgency: self.urgency,
            occasion: self.occasion,
            expire_ms: (self.expire_ms >= 0).then_some(self.expire_ms as u32),
            report: self.report,
            focus: self.focus,
            close_event: self.close_event,
        }
    }
}

fn parse_osc99_meta(meta: &str) -> Vec<(char, String)> {
    meta.split(':')
        .filter_map(|part| {
            let (k, v) = part.split_once('=')?;
            let mut ks = k.chars();
            let c = ks.next()?;
            ks.next().is_none().then(|| (c, v.to_string()))
        })
        .collect()
}

fn last(kv: &[(char, String)], k: char) -> Option<&str> {
    kv.iter().rev().find(|(kk, _)| *kk == k).map(|(_, v)| v.as_str())
}

fn named(raw: &str, ok: fn(&str) -> bool) -> Option<String> {
    if ok(raw) {
        return Some(raw.to_string());
    }
    let dec = decode_osc99_payload(raw, true);
    ok(&dec).then_some(dec)
}

fn is_sound_name(s: &str) -> bool {
    matches!(s, "system" | "silent" | "error" | "warn" | "warning" | "info" | "question")
}

fn is_icon_name(s: &str) -> bool {
    matches!(s, "error" | "warn" | "warning" | "info" | "question" | "help")
}

fn decode_osc99_payload(s: &str, b64: bool) -> String {
    if !b64 {
        return strip_controls(s);
    }
    let Some(mut raw) = decode_b64(s.as_bytes()) else { return String::new() };
    raw.truncate(8192);
    strip_controls(&String::from_utf8_lossy(&raw))
}

fn strip_controls(s: &str) -> String {
    s.chars().filter(|&c| !(c < ' ' || c == '\x7f' || ('\u{80}'..='\u{9f}').contains(&c))).collect()
}

fn sanitize_notice_id(id: &str) -> String {
    if id.is_empty() || id == "0" {
        return String::new();
    }
    id.chars().filter(|c| c.is_ascii_alphanumeric() || matches!(c, '_' | '-' | '+' | '.')).collect()
}

fn osc99_query_reply(id: &str) -> Vec<u8> {
    let id = if id.is_empty() { "0" } else { id };
    format!(
        "\x1b]99;i={id}:p=?;a=focus,report:c=1:o=always,unfocused,invisible:p=title,body,close,alive,?:s=system,silent,error,warn,warning,info,question:u=0,1,2:w=1\x1b\\"
    )
    .into_bytes()
}

/// OSC 99 p=alive answer: the ids of this program's notices still showing.
pub fn alive_reply(id: &str, live: &[String]) -> Vec<u8> {
    let id = if id.is_empty() { "0" } else { id };
    format!("\x1b]99;i={id}:p=alive;{}\x1b\\", live.join(",")).into_bytes()
}

/// OSC 99 events a clicked or closed notice sends back (Go activateNotice,
/// sendNoticeClose).
pub fn activate_report(id: &str) -> Vec<u8> {
    let id = if id.is_empty() { "0" } else { id };
    format!("\x1b]99;i={id}\x1b\\").into_bytes()
}

pub fn close_report(id: &str) -> Vec<u8> {
    format!("\x1b]99;i={id}:p=;\x1b\\").into_bytes()
}

// OSC 1337 File=[args]:<base64> (Go parseImageOSC). The payload follows the
// last colon; inline=0 means "download", which isn't shown.
fn parse_iterm_file(rest: &str) -> Option<(String, Vec<u8>)> {
    let colon = rest.rfind(':')?;
    let (args, b64) = (&rest[..colon], &rest[colon + 1..]);
    if b64.is_empty() {
        return None;
    }
    let mut inline = true;
    let mut name = "inline".to_string();
    for part in args.split(';').map(str::trim).filter(|p| !p.is_empty()) {
        let (k, v) = part.split_once('=').unwrap_or((part, ""));
        match k.trim().to_ascii_lowercase().as_str() {
            "inline" => inline = v.trim() == "1" || v.trim().eq_ignore_ascii_case("true"),
            "name" => {
                let v = v.trim();
                name = match STANDARD.decode(v) {
                    Ok(d) if !d.is_empty() => String::from_utf8_lossy(&d).into_owned(),
                    _ if !v.is_empty() => v.to_string(),
                    _ => name,
                };
            }
            _ => {}
        }
    }
    if !inline {
        return None;
    }
    let data = decode_b64(b64.as_bytes())?;
    (!data.is_empty() && data.len() <= MAX_IMAGE_BYTES).then_some((name, data))
}

fn decode_b64(b: &[u8]) -> Option<Vec<u8>> {
    let clean: Vec<u8> = b.iter().copied().filter(|c| !matches!(c, b'\n' | b'\r' | b' ' | b'\t')).collect();
    STANDARD.decode(&clean).or_else(|_| STANDARD_NO_PAD.decode(&clean)).ok()
}

fn parse_kitty_kv(header: &str) -> Vec<(String, String)> {
    header
        .split(',')
        .filter_map(|p| {
            let (k, v) = p.trim().split_once('=')?;
            Some((k.trim().to_string(), v.trim().to_string()))
        })
        .collect()
}

fn clip(s: &str, n: usize) -> String {
    s.chars().take(n).collect()
}

fn dedup(v: Vec<String>) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    for s in v {
        if !out.contains(&s) {
            out.push(s);
        }
    }
    out
}

// Go's reImagePath: absolute Windows or Unix image paths, and paths through
// an images/ or .grok folder (Grok Imagine and other agents print these).
fn image_path_re() -> &'static regex::Regex {
    static RE: std::sync::OnceLock<regex::Regex> = std::sync::OnceLock::new();
    RE.get_or_init(|| {
        regex::Regex::new(concat!(
            r#"(?i)(?:"#,
            r#"[a-z]:[\\/][^\s"'<>|*?\x1b]+\.(?:png|jpe?g|gif|webp)|"#,
            r#"/[^\s"'<>|*?\x1b]+\.(?:png|jpe?g|gif|webp)|"#,
            r#"(?:images|\.grok)[\\/][^\s"'<>|*?\x1b]+\.(?:png|jpe?g|gif|webp)|"#,
            r#"[^\s"'<>|*?\x1b]+[\\/]images[\\/][^\s"'<>|*?\x1b]+\.(?:png|jpe?g|gif|webp)"#,
            r#")"#
        ))
        .expect("image path regex")
    })
}

/// Image paths printed in `s` (Go findImagePathsInText), at most 12.
pub fn find_image_paths(s: &str) -> Vec<String> {
    let low = s.to_ascii_lowercase();
    if ![".png", ".jpg", ".jpeg", ".gif", ".webp"].iter().any(|e| low.contains(e)) {
        return Vec::new();
    }
    image_path_re()
        .find_iter(s)
        .take(12)
        .map(|m| m.as_str().trim_matches(|c| "\"'()[]".contains(c)).to_string())
        .filter(|m| !m.is_empty())
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Default)]
    struct Rec {
        text: Vec<u8>,
        events: Vec<ProtoEvent>,
        order: Vec<String>,
    }

    impl ProtoSink for Rec {
        fn text(&mut self, b: &[u8]) {
            self.text.extend_from_slice(b);
            self.order.push(format!("t:{}", String::from_utf8_lossy(b)));
        }
        fn event(&mut self, ev: ProtoEvent) {
            self.order.push("e".into());
            self.events.push(ev);
        }
    }

    fn run(chunks: &[&[u8]]) -> Rec {
        let mut p = Protocols { scan_paths: true, ..Protocols::new() };
        let mut r = Rec::default();
        for c in chunks {
            p.feed(c, &mut r);
        }
        r
    }

    fn notice(r: &Rec) -> &Notice {
        r.events.iter().find_map(|e| if let ProtoEvent::Notice(n) = e { Some(n) } else { None }).expect("a notice")
    }

    #[test]
    fn other_sequences_pass_through() {
        let input = b"a\x1b]0;title\x07b\x1b]8;;http://x\x1b\\c\x1b[1md\x1b_Xfoo\x1b\\e";
        let r = run(&[input]);
        assert_eq!(r.text, input);
        assert!(r.events.is_empty());
    }

    #[test]
    fn osc9_notice_and_progress() {
        let r = run(&[b"x\x1b]9;build done \x07y"]);
        assert_eq!(r.text, b"xy");
        let n = notice(&r);
        assert_eq!((n.title.as_str(), n.body.as_str(), n.sound.as_str()), ("Suzuri", "build done", "info"));
        let r = run(&[b"\x1b]9;4;1;150\x1b\\"]);
        assert_eq!(r.events, vec![ProtoEvent::Progress { state: 1, percent: 100 }]);
    }

    #[test]
    fn osc777_notify() {
        let r = run(&[b"\x1b]777;notify;Claude;needs you\x07"]);
        let n = notice(&r);
        assert_eq!((n.title.as_str(), n.body.as_str()), ("Claude", "needs you"));
        let r = run(&[b"\x1b]777;notify;only title\x07"]);
        assert_eq!((notice(&r).title.as_str(), notice(&r).body.as_str()), ("Suzuri", "only title"));
        assert!(run(&[b"\x1b]777;other;x\x07"]).events.is_empty());
    }

    #[test]
    fn osc99_chunks_and_meta() {
        let r = run(&[
            b"\x1b]99;i=job:d=0:u=2:s=error:n=error;Build\x1b\\",
            b"\x1b]99;i=job:p=body:a=report;failed at step 3\x1b\\",
        ]);
        let n = notice(&r);
        assert_eq!(n.id, "job");
        assert_eq!((n.title.as_str(), n.body.as_str()), ("Build", "failed at step 3"));
        assert_eq!((n.urgency, n.sound.as_str(), n.icon.as_str()), (2, "error", "error"));
        assert!(n.report && !n.focus);
        assert_eq!(r.events.len(), 1);

        let b64 = STANDARD.encode("héllo");
        let r = run(&[format!("\x1b]99;e=1:w=0;{b64}\x1b\\").as_bytes()]);
        assert_eq!(notice(&r).title, "héllo");
        assert_eq!(notice(&r).expire_ms, Some(0));
    }

    #[test]
    fn osc99_query_alive_close() {
        let r = run(&[b"\x1b]99;i=a:p=?;\x1b\\\x1b]99;i=b:p=alive;\x1b\\\x1b]99;i=c:p=close;\x1b\\"]);
        assert!(matches!(&r.events[0], ProtoEvent::Reply(b) if b.starts_with(b"\x1b]99;i=a:p=?;")));
        assert_eq!(r.events[1], ProtoEvent::AliveQuery("b".into()));
        assert_eq!(r.events[2], ProtoEvent::CloseNotice("c".into()));
    }

    #[test]
    fn iterm_inline_image() {
        let png = b"\x89PNG\r\n\x1a\nfake";
        let name = STANDARD.encode("cat.png");
        let seq = format!("\x1b]1337;File=name={name};size=12;inline=1:{}\x07", STANDARD.encode(png));
        let r = run(&[b"before", seq.as_bytes(), b"after"]);
        assert_eq!(r.text, b"beforeafter");
        assert_eq!(r.events, vec![ProtoEvent::InlineImage { name: "cat.png".into(), data: png.to_vec() }]);
        let off = format!("\x1b]1337;File=inline=0:{}\x07", STANDARD.encode(png));
        assert!(run(&[off.as_bytes()]).events.is_empty());
        // Other 1337 sequences are not ours.
        let r = run(&[b"\x1b]1337;SetUserVar=a=Yg==\x07"]);
        assert!(r.events.is_empty() && !r.text.is_empty());
    }

    #[test]
    fn split_sequences_are_held() {
        let png = STANDARD.encode(b"\x89PNG\r\n\x1a\nxyz");
        let seq = format!("A\x1b]1337;File=inline=1:{png}\x1b\\B");
        let bytes = seq.as_bytes();
        // Every split point, including inside the ESC \ terminator.
        for cut in 1..bytes.len() {
            let r = run(&[&bytes[..cut], &bytes[cut..]]);
            assert_eq!(r.text, b"AB", "cut at {cut}");
            assert_eq!(r.events.len(), 1, "cut at {cut}");
        }
    }

    #[test]
    fn kitty_chunks_place_delete() {
        let payload = STANDARD.encode(b"\x89PNG\r\n\x1a\n0123456789");
        let (a, b) = payload.split_at(8);
        let r = run(&[
            b"x",
            format!("\x1b_Ga=T,i=7,f=100,c=4,r=2,m=1;{a}\x1b\\").as_bytes(),
            b"\x1b_Gm=1;",
            format!("{b}\x1b\\").as_bytes(),
            b"\x1b_Gm=0;\x1b\\y",
        ]);
        assert_eq!(r.text, b"xy");
        assert_eq!(r.order, vec!["t:x", "e", "t:y"]);
        match &r.events[0] {
            ProtoEvent::Kitty(Kitty::Image { id, format, data, place, .. }) => {
                assert_eq!((*id, *format), (7, 100));
                assert_eq!(data, b"\x89PNG\r\n\x1a\n0123456789");
                assert_eq!(place.map(|p| (p.cols, p.rows)), Some((4, 2)));
            }
            e => panic!("{e:?}"),
        }
        let r = run(&[b"\x1b_Ga=p,i=7\x1b\\\x1b_Ga=d,d=A\x1b\\\x1b_Ga=d,i=3\x1b\\"]);
        assert_eq!(r.events[0], ProtoEvent::Kitty(Kitty::Place { id: 7, place: KittyPlace { cols: 1, rows: 1, z: 1, ..Default::default() } }));
        assert_eq!(r.events[1], ProtoEvent::Kitty(Kitty::Delete { id: None }));
        assert_eq!(r.events[2], ProtoEvent::Kitty(Kitty::Delete { id: Some(3) }));
    }

    #[test]
    fn trailing_esc_is_held_not_printed() {
        let r = run(&[b"ab\x1b", b"_Ga=d,d=A\x1b\\cd"]);
        assert_eq!(r.text, b"abcd");
        let r = run(&[b"ab\x1b", b"[1mcd"]);
        assert_eq!(r.text, b"ab\x1b[1mcd");
    }

    #[test]
    fn image_paths_in_text() {
        assert_eq!(find_image_paths("saved to /Users/me/out/cat.PNG ok"), vec!["/Users/me/out/cat.PNG"]);
        assert_eq!(find_image_paths(r"C:\Users\me\.grok\x.jpeg."), vec![r"C:\Users\me\.grok\x.jpeg"]);
        assert_eq!(find_image_paths("images/1.jpg"), vec!["images/1.jpg"]);
        assert!(find_image_paths("cat.png").is_empty());
        let r = run(&[b"\x1b]7879;image=/tmp/x.png\x07"]);
        assert_eq!(r.events, vec![ProtoEvent::ImagePath("/tmp/x.png".into())]);
    }
}
