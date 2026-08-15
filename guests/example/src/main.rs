//! Sample suzuri guest. Chrome never links this crate — it is a separate
//! process that speaks line-delimited JSON over one TCP connection.

use std::env;
use std::io::{self, BufRead, BufReader, Write};
use std::net::TcpStream;
use std::process;

const HELLO: &[u8] = b"{\"type\":\"hello\",\"protocol\":1,\"title\":\"Example\"}\n";

fn main() {
    let port = match resolve_port(env::args().skip(1), env::var("SUZURI_GUEST_PORT").ok()) {
        Ok(port) => port,
        Err(msg) => {
            eprintln!("suzuri-guest-example: {msg}");
            process::exit(1);
        }
    };

    let mut stream = match TcpStream::connect(("127.0.0.1", port)) {
        Ok(stream) => stream,
        Err(err) => {
            eprintln!("suzuri-guest-example: connect 127.0.0.1:{port} failed: {err}");
            process::exit(1);
        }
    };

    if let Err(err) = stream.write_all(HELLO).and_then(|_| stream.flush()) {
        eprintln!("suzuri-guest-example: write hello failed: {err}");
        process::exit(1);
    }

    let _ = serve(stream);
}

struct FbSlot {
    path: String,
    w: u32,
    h: u32,
}

fn serve(stream: TcpStream) -> io::Result<()> {
    let mut writer = stream.try_clone()?;
    let mut fb: Option<FbSlot> = None;
    let mut url = String::new();
    for line in BufReader::new(stream).lines() {
        let line = line?;
        if line.is_empty() {
            continue;
        }
        match message_type(&line).as_deref() {
            Some("spawn") | Some("resize") => {
                if let Some(next) = parse_fb(&line) {
                    fb = Some(next);
                }
                if let Some(slot) = &fb {
                    let _ = paint_fb(slot, &url);
                    write_line(&mut writer, &surface_msg(slot))?;
                }
            }
            Some("stack") | Some("focus") | Some("draft") => {}
            Some("navigate") => {
                url = extract_url(&line);
                write_line(&mut writer, &field_msg("title", &url))?;
                write_line(&mut writer, &field_msg("url", &url))?;
                if let Some(slot) = &fb {
                    let _ = paint_fb(slot, &url);
                    write_line(&mut writer, &surface_msg(slot))?;
                }
            }
            Some("kill") => return Ok(()),
            _ => {}
        }
    }
    Ok(())
}

/// Guest-owned jade rail (BGRA). Chrome tests this first pixel.
const RAIL: [u8; 4] = [74, 186, 61, 255];
const FILL: [u8; 4] = [18, 28, 16, 255];
const BAR: [u8; 4] = [28, 42, 24, 255];

fn parse_fb(line: &str) -> Option<FbSlot> {
    let path = json_string_field(line, "path")?;
    if path.is_empty() {
        return None;
    }
    let w = json_i64_field(line, "width")? as u32;
    let h = json_i64_field(line, "height")? as u32;
    if w == 0 || h == 0 || w > 4096 || h > 4096 {
        return None;
    }
    Some(FbSlot { path, w, h })
}

fn surface_msg(fb: &FbSlot) -> String {
    let mut out = String::from("{\"type\":\"surface\",\"path\":");
    push_json_string(&mut out, &fb.path);
    out.push_str(&format!(",\"width\":{},\"height\":{}}}", fb.w, fb.h));
    out
}

fn paint_fb(fb: &FbSlot, url: &str) -> io::Result<()> {
    let w = fb.w as usize;
    let h = fb.h as usize;
    let mut px = vec![0u8; w * h * 4];
    let stripe = url_stripe(url);
    let rail_w = 8.min(w);
    let bar_h = 6.min(h);
    let band_y = 16.min(h.saturating_sub(1));
    let band_h = 4.min(h.saturating_sub(band_y));
    for y in 0..h {
        for x in 0..w {
            let c = if x < rail_w {
                RAIL
            } else if y < bar_h {
                BAR
            } else if y >= band_y && y < band_y + band_h {
                stripe
            } else {
                FILL
            };
            let i = (y * w + x) * 4;
            px[i..i + 4].copy_from_slice(&c);
        }
    }
    let seq = read_seq(&fb.path).unwrap_or(0).wrapping_add(1);
    write_szfb(&fb.path, fb.w, fb.h, seq, &px)
}

fn url_stripe(url: &str) -> [u8; 4] {
    let mut h: u32 = 2166136261;
    for b in url.as_bytes() {
        h ^= u32::from(*b);
        h = h.wrapping_mul(16777619);
    }
    [
        40 + (h & 0x3f) as u8,
        90 + ((h >> 8) & 0x5f) as u8,
        50 + ((h >> 16) & 0x3f) as u8,
        255,
    ]
}

fn read_seq(path: &str) -> Option<u32> {
    let mut f = std::fs::File::open(path).ok()?;
    let mut hdr = [0u8; 16];
    use std::io::Read;
    f.read_exact(&mut hdr).ok()?;
    if &hdr[0..4] != b"SZFB" {
        return None;
    }
    Some(u32::from_le_bytes(hdr[12..16].try_into().ok()?))
}

fn write_szfb(path: &str, w: u32, h: u32, seq: u32, bgra: &[u8]) -> io::Result<()> {
    use std::io::{Seek, SeekFrom};
    let n = (w as usize) * (h as usize) * 4;
    let mut f = std::fs::OpenOptions::new()
        .create(true)
        .write(true)
        .read(true)
        .open(path)?;
    f.set_len((16 + n) as u64)?;
    f.seek(SeekFrom::Start(16))?;
    f.write_all(&bgra[..n])?;
    let mut hdr = [0u8; 16];
    hdr[0..4].copy_from_slice(b"SZFB");
    hdr[4..8].copy_from_slice(&w.to_le_bytes());
    hdr[8..12].copy_from_slice(&h.to_le_bytes());
    hdr[12..16].copy_from_slice(&seq.to_le_bytes());
    f.seek(SeekFrom::Start(0))?;
    f.write_all(&hdr)?;
    f.flush()
}

fn write_line(w: &mut impl Write, line: &str) -> io::Result<()> {
    writeln!(w, "{line}")?;
    w.flush()
}

fn field_msg(kind: &str, value: &str) -> String {
    let mut out = String::from("{\"type\":\"");
    out.push_str(kind);
    out.push_str("\",\"string\":");
    push_json_string(&mut out, value);
    out.push('}');
    out
}

/// `--port N` / `--port=N` wins over `SUZURI_GUEST_PORT`. `--suzuri-guest` is ignored.
fn resolve_port(
    args: impl IntoIterator<Item = impl AsRef<str>>,
    env_port: Option<String>,
) -> Result<u16, String> {
    let mut port = None;
    let mut args = args.into_iter();
    while let Some(arg) = args.next() {
        let arg = arg.as_ref();
        if arg == "--suzuri-guest" {
            continue;
        }
        if arg == "--port" {
            let value = args
                .next()
                .ok_or_else(|| "missing value for --port".to_string())?;
            port = Some(parse_port(value.as_ref())?);
            continue;
        }
        if let Some(value) = arg.strip_prefix("--port=") {
            port = Some(parse_port(value)?);
        }
    }
    if let Some(port) = port {
        return Ok(port);
    }
    match env_port {
        Some(value) => parse_port(&value),
        None => Err("need --port N or SUZURI_GUEST_PORT".into()),
    }
}

fn parse_port(s: &str) -> Result<u16, String> {
    match s.parse::<u16>() {
        Ok(0) | Err(_) => Err(format!("invalid port {s:?}")),
        Ok(port) => Ok(port),
    }
}

fn message_type(line: &str) -> Option<String> {
    json_string_field(line, "type")
}

/// `"url"` JSON string, or empty when the field is missing / not a string.
fn extract_url(line: &str) -> String {
    json_string_field(line, "url").unwrap_or_default()
}

fn json_object_slice<'a>(line: &'a str, key: &str) -> Option<&'a str> {
    let after = after_key(line, key)?;
    let rest = after.trim_start().strip_prefix('{')?;
    let mut depth = 1i32;
    for (i, c) in rest.char_indices() {
        match c {
            '{' => depth += 1,
            '}' => {
                depth -= 1;
                if depth == 0 {
                    return Some(&rest[..i]);
                }
            }
            _ => {}
        }
    }
    None
}

fn json_bool_field(line: &str, key: &str) -> Option<bool> {
    let after = after_key(line, key)?.trim_start();
    if after.starts_with("true") {
        Some(true)
    } else if after.starts_with("false") {
        Some(false)
    } else {
        None
    }
}

fn json_i64_field(line: &str, key: &str) -> Option<i64> {
    json_f64_field(line, key).map(|n| n as i64)
}

fn json_f64_field(line: &str, key: &str) -> Option<f64> {
    let after = after_key(line, key)?.trim_start();
    let mut end = 0;
    let bytes = after.as_bytes();
    if bytes.first() == Some(&b'-') {
        end = 1;
    }
    while end < bytes.len() && (bytes[end].is_ascii_digit() || bytes[end] == b'.') {
        end += 1;
    }
    if end == 0 || after[..end] == *"-" {
        return None;
    }
    after[..end].parse().ok()
}

fn after_key<'a>(line: &'a str, key: &str) -> Option<&'a str> {
    let mut search = line;
    while let Some(idx) = search.find('"') {
        search = &search[idx + 1..];
        let Some(after_key) = search.strip_prefix(key).and_then(|s| s.strip_prefix('"')) else {
            continue;
        };
        return after_key.trim_start().strip_prefix(':');
    }
    None
}

fn json_string_field(line: &str, key: &str) -> Option<String> {
    let mut search = line;
    while let Some(idx) = search.find('"') {
        search = &search[idx + 1..];
        let Some(after_key) = search.strip_prefix(key).and_then(|s| s.strip_prefix('"')) else {
            continue;
        };
        let Some(after_colon) = after_key.trim_start().strip_prefix(':') else {
            continue;
        };
        let after_quote = after_colon.trim_start().strip_prefix('"')?;
        return parse_json_string(after_quote);
    }
    None
}

fn parse_json_string(after_open_quote: &str) -> Option<String> {
    let mut out = String::new();
    let mut chars = after_open_quote.chars();
    while let Some(c) = chars.next() {
        match c {
            '"' => return Some(out),
            '\\' => match chars.next()? {
                '"' => out.push('"'),
                '\\' => out.push('\\'),
                '/' => out.push('/'),
                'n' => out.push('\n'),
                'r' => out.push('\r'),
                't' => out.push('\t'),
                'u' => {
                    let hex: String = chars.by_ref().take(4).collect();
                    if hex.len() != 4 {
                        return None;
                    }
                    let code = u32::from_str_radix(&hex, 16).ok()?;
                    out.push(char::from_u32(code)?);
                }
                other => out.push(other),
            },
            c => out.push(c),
        }
    }
    None
}

fn push_json_string(out: &mut String, s: &str) {
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if c.is_control() => {
                let n = c as u32;
                out.push_str(&format!("\\u{n:04x}"));
            }
            c => out.push(c),
        }
    }
    out.push('"');
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn detects_message_type() {
        let cases: &[(&str, Option<&str>)] = &[
            (
                r#"{"type":"hello","protocol":1,"title":"Example"}"#,
                Some("hello"),
            ),
            (
                r#"{"type":"spawn","pane_id":"p1","rect":"0 0 80 24","scale":1,"cwd":"/tmp"}"#,
                Some("spawn"),
            ),
            (
                r#"{"type":"resize","rect":"0 0 100 40","scale":2}"#,
                Some("resize"),
            ),
            (r#"{"type":"focus","in":true}"#, Some("focus")),
            (
                r#"{"type":"navigate","url":"https://example.com"}"#,
                Some("navigate"),
            ),
            (r#"{ "type" : "kill" }"#, Some("kill")),
            (r#"{"type":"draft","string":"ab"}"#, Some("draft")),
            (r#"{"type":"nope"}"#, Some("nope")),
            (r#"{"nope":true}"#, None),
            ("", None),
        ];
        for (line, want) in cases {
            assert_eq!(message_type(line).as_deref(), *want, "{line}");
        }
    }

    #[test]
    fn extracts_url() {
        let cases = [
            (
                r#"{"type":"navigate","url":"https://example.com"}"#,
                "https://example.com",
            ),
            (r#"{"type":"navigate"}"#, ""),
            (r#"{"type":"navigate","url":""}"#, ""),
            (r#"{ "url" : "x y" }"#, "x y"),
            (r#"{"type":"navigate","url":"a\"b"}"#, "a\"b"),
            (r#"{"url":"c:\\tmp"}"#, "c:\\tmp"),
            (r#"{"url":"https://ex.com/\u0041"}"#, "https://ex.com/A"),
            (r#"{"type":"spawn"}"#, ""),
        ];
        for (line, want) in cases {
            assert_eq!(extract_url(line), want, "{line}");
        }
    }

    #[test]
    fn reads_native_attach() {
        let line = r#"{"type":"resize","rect":{"x":1,"y":2,"w":3,"h":4},"visible":false,"native":{"kind":"nswindow","window_number":99,"screen":{"x":10.5,"y":20,"w":30,"h":40}}}"#;
        assert_eq!(json_bool_field(line, "visible"), Some(false));
        assert_eq!(json_i64_field(line, "window_number"), Some(99));
        let screen = json_object_slice(line, "screen").unwrap();
        assert_eq!(json_f64_field(screen, "x"), Some(10.5));
        assert_eq!(json_f64_field(screen, "h"), Some(40.0));
    }

    #[test]
    fn parses_fb_and_paints_rail() {
        let line = r#"{"type":"spawn","fb":{"path":"/tmp/x.szfb","width":32,"height":16}}"#;
        let fb = parse_fb(line).unwrap();
        assert_eq!(fb.w, 32);
        assert_eq!(fb.h, 16);
        let p = std::env::temp_dir().join(format!("suzuri-ex-fb-{}", std::process::id()));
        let slot = FbSlot {
            path: p.to_string_lossy().into(),
            w: 16,
            h: 8,
        };
        paint_fb(&slot, "https://echo.test/").unwrap();
        let mut hdr = [0u8; 16];
        let mut f = std::fs::File::open(&p).unwrap();
        use std::io::Read;
        f.read_exact(&mut hdr).unwrap();
        assert_eq!(&hdr[0..4], b"SZFB");
        let mut px = vec![0u8; 16 * 8 * 4];
        f.read_exact(&mut px).unwrap();
        assert_eq!(&px[0..4], &RAIL);
        let _ = std::fs::remove_file(&p);
    }
}
