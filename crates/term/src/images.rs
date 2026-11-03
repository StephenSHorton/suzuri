//! A pane's images (card #288): inline images (OSC 1337, OSC 7879, image
//! paths printed as text) and Kitty graphics, and where they sit on screen.
//!
//! Like Go suzuri (tab.ingestImages, kittyGfxState):
//! - an inline image goes under the latest output on the main screen, as a
//!   block of rows plus a caption row, at most 8 per pane; full-screen apps
//!   get none;
//! - a Kitty image shows at the cursor, at most 16 per pane.
//!
//! On the main screen an image is anchored by a mark in its top-left cell
//! (Engine::mark_cursor), so it scrolls and clears with the text around it.
//! In a full-screen app it is anchored to the screen row and dropped when
//! the app leaves the alternate screen.

use std::path::{Path, PathBuf};

use crate::protocols::{KittyPlace, MAX_IMAGE_BYTES};

const MAX_INLINE: usize = 8;
const MAX_KITTY: usize = 16;
/// Rows above the view still scanned for marks: an image's top may be above
/// the view while its lower part shows (Go caps a block at 40 rows).
pub const MARK_REACH: usize = 64;

/// Image bytes for the UI, sent once per image.
#[derive(Debug, Clone, PartialEq)]
pub struct ImageData {
    pub id: u32,
    /// The file name or path shown in the caption and the large view.
    pub name: String,
    /// The file it came from, when known (open on click).
    pub path: Option<String>,
    /// 100: an encoded file (PNG, JPEG, GIF, WebP); 24 or 32: raw RGB/RGBA.
    pub format: u32,
    pub width: u32,
    pub height: u32,
    /// zlib (Kitty o=z).
    pub compressed: bool,
    pub data: Vec<u8>,
}

/// One image on screen, for the UI to draw over the cells.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Placement {
    pub image: u32,
    /// Top-left cell; row is relative to the view's top and may be negative.
    pub row: i32,
    pub col: u16,
    pub cols: u32,
    pub rows: u32,
    pub z: i32,
    /// Source crop in pixels (w/h 0: all of it).
    pub crop: (u32, u32, u32, u32),
    /// An inline image (caption below, click to enlarge).
    pub inline: bool,
}

#[derive(Debug, Clone, Copy, PartialEq)]
enum Anchor {
    /// A mark in the grid, with this tag.
    Mark(u32),
    /// A screen row and column in the alternate screen.
    Screen(u16, u16),
}

#[derive(Debug)]
struct Shown {
    image: u32,
    anchor: Anchor,
    cols: u32,
    rows: u32,
    z: i32,
    crop: (u32, u32, u32, u32),
    inline: bool,
}

#[derive(Default)]
pub struct Images {
    next: u32,
    /// Inline images, oldest first: (image id, canonical key).
    inline: Vec<(u32, String)>,
    /// Kitty image id -> our image id, oldest first.
    kitty: Vec<(u32, u32, u32, u32)>,
    shown: Vec<Shown>,
    last: Vec<Placement>,
}

/// What a change to the store asks the pane to send.
#[derive(Debug, Default, PartialEq)]
pub struct Changes {
    pub data: Vec<ImageData>,
    pub dropped: Vec<u32>,
}

impl Images {
    pub fn is_empty(&self) -> bool {
        self.shown.is_empty()
    }

    fn id(&mut self) -> u32 {
        self.next += 1;
        self.next
    }

    /// An inline image placed at a new mark; returns its id. Same file again
    /// moves it to the newest place (Go imageStore.addAt).
    pub fn add_inline(&mut self, key: &str, mark: u32, cols: u32, rows: u32, out: &mut Changes) -> u32 {
        let canon = canon_key(key);
        if let Some(at) = self.inline.iter().position(|(_, k)| *k == canon) {
            let (old, _) = self.inline.remove(at);
            self.forget(old, out);
        }
        let id = self.id();
        self.inline.push((id, canon));
        self.shown.push(Shown { image: id, anchor: Anchor::Mark(mark), cols, rows, z: 0, crop: (0, 0, 0, 0), inline: true });
        while self.inline.len() > MAX_INLINE {
            let (old, _) = self.inline.remove(0);
            self.forget(old, out);
        }
        id
    }

    /// A Kitty image arrived: (our id, whether it replaced one).
    pub fn kitty_image(&mut self, kid: u32, width: u32, height: u32, out: &mut Changes) -> u32 {
        if let Some(at) = self.kitty.iter().position(|k| k.0 == kid) {
            let (_, old, _, _) = self.kitty.remove(at);
            self.forget(old, out);
        }
        let id = self.id();
        self.kitty.push((kid, id, width, height));
        while self.kitty.len() > MAX_KITTY {
            let (_, old, _, _) = self.kitty.remove(0);
            self.forget(old, out);
        }
        id
    }

    /// Show Kitty image `kid` (a=T or a=p), replacing its other placement.
    /// `mark` on the main screen, else the screen cell.
    pub fn kitty_place(&mut self, kid: u32, place: KittyPlace, mark: Option<u32>, cell: (u16, u16)) {
        let Some(&(_, id, _, _)) = self.kitty.iter().find(|k| k.0 == kid) else { return };
        self.shown.retain(|s| s.image != id);
        let anchor = match mark {
            Some(t) => Anchor::Mark(t),
            None => Anchor::Screen(cell.1, cell.0),
        };
        self.shown.push(Shown {
            image: id,
            anchor,
            cols: place.cols,
            rows: place.rows,
            z: place.z,
            crop: (place.x, place.y, place.w, place.h),
            inline: false,
        });
    }

    /// Kitty a=d: one image (by Kitty id) or all of them.
    pub fn kitty_delete(&mut self, kid: Option<u32>, out: &mut Changes) {
        let gone: Vec<u32> = self.kitty.iter().filter(|k| kid.is_none_or(|kid| k.0 == kid)).map(|k| k.1).collect();
        self.kitty.retain(|k| !gone.contains(&k.1));
        for id in gone {
            self.forget(id, out);
        }
    }

    fn forget(&mut self, id: u32, out: &mut Changes) {
        self.shown.retain(|s| s.image != id);
        out.data.retain(|d| d.id != id);
        out.dropped.push(id);
    }

    /// The images on screen now, from the engine's marks. Screen-anchored
    /// ones go once the alternate screen is left. Some only when it changed
    /// since the last call.
    pub fn placements(&mut self, marks: &[(u32, i32, u16)], alt: bool) -> Option<Vec<Placement>> {
        if !alt {
            self.shown.retain(|s| !matches!(s.anchor, Anchor::Screen(..)));
        }
        let mut now = Vec::new();
        for s in &self.shown {
            let (row, col) = match s.anchor {
                Anchor::Mark(tag) => match marks.iter().find(|m| m.0 == tag) {
                    Some(&(_, row, col)) => (row, col),
                    None => continue,
                },
                Anchor::Screen(row, col) => (row as i32, col),
            };
            if row + (s.rows as i32) <= 0 {
                continue;
            }
            now.push(Placement { image: s.image, row, col, cols: s.cols, rows: s.rows, z: s.z, crop: s.crop, inline: s.inline });
        }
        if now == self.last {
            return None;
        }
        self.last = now.clone();
        Some(now)
    }

    /// The next mark tag (shares the id counter, so tags never repeat).
    pub fn tag(&mut self) -> u32 {
        self.id()
    }
}

fn canon_key(key: &str) -> String {
    let p = Path::new(key);
    let clean: PathBuf = p.components().collect();
    clean.to_string_lossy().to_lowercase()
}

/// Rows and columns an inline image takes (Go tab.ingestImages and
/// scrollback.pushImage): shown at its own size when it fits the pane's
/// width (at most 2400 px) and 39 rows, else scaled down; at least 2 rows.
/// The caption row comes on top of `rows`.
pub fn inline_cells(px: (u32, u32), cell: (u32, u32), pane_cols: u32) -> (u32, u32) {
    let (cw, ch) = (cell.0.max(1), cell.1.max(1));
    let cols = if pane_cols < 20 { 80 } else { pane_cols };
    let max_w = (cols * cw).min(2400);
    // Go caps the height at 64 rows, then the block (with its caption) at
    // 40: scale to 39 so the image keeps its shape.
    let max_h = 39 * ch;
    let (dw, dh) = fit(px.0, px.1, max_w, max_h);
    let rows = dh.div_ceil(ch).clamp(2, 39);
    let cols = dw.div_ceil(cw).clamp(1, cols);
    (cols, rows)
}

fn fit(w: u32, h: u32, max_w: u32, max_h: u32) -> (u32, u32) {
    if w == 0 || h == 0 {
        return (max_w.min(320), max_h.min(240));
    }
    if w <= max_w && h <= max_h {
        return (w, h);
    }
    let s = (max_w as f64 / w as f64).min(max_h as f64 / h as f64);
    (((w as f64 * s) as u32).max(1), ((h as f64 * s) as u32).max(1))
}

/// The pixel size of an encoded image, from its header.
pub fn pixel_size(data: &[u8]) -> Option<(u32, u32)> {
    let s = imagesize::blob_size(data).ok()?;
    Some((s.width as u32, s.height as u32))
}

/// Find an image path printed by a program (Go resolveImagePath): as given,
/// under the pane's folder, under ~/.grok, and Grok's session folders.
pub fn resolve_path(cwd: Option<&Path>, home: Option<&Path>, reference: &str) -> Option<PathBuf> {
    let mut r = reference.trim().to_string();
    if r.is_empty() {
        return None;
    }
    if r.to_ascii_lowercase().starts_with("file:") {
        r = file_uri_path(&r)?;
    }
    let r = fix_grok_session_path(&r);
    let mut tries = vec![PathBuf::from(&r)];
    let rp = Path::new(&r);
    if let Some(cwd) = cwd.filter(|_| !rp.is_absolute()) {
        tries.push(cwd.join(rp));
    }
    if let Some(home) = home {
        tries.push(home.join(".grok").join(rp));
    }
    if let Some(hit) = tries.into_iter().find(|p| p.is_file()) {
        return Some(hit);
    }
    let sessions = home?.join(".grok").join("sessions");
    if let Some((uuid, base)) = session_image(&r) {
        let hit = std::fs::read_dir(&sessions).ok()?.flatten().map(|e| e.path().join(&uuid).join("images").join(&base)).find(|p| p.is_file());
        if hit.is_some() {
            return hit;
        }
    }
    let base = rp.file_name()?.to_string_lossy().into_owned();
    is_image_name(&base).then(|| find_under(&sessions, &base, 5)).flatten()
}

fn file_uri_path(u: &str) -> Option<String> {
    let rest = &u[5..];
    let rest = rest.strip_prefix("//").map(|r| r.find('/').map_or("", |i| &r[i..])).unwrap_or(rest);
    let mut out = Vec::new();
    let b = rest.as_bytes();
    let mut i = 0;
    while i < b.len() {
        if b[i] == b'%' && i + 2 < b.len() {
            if let Ok(v) = u8::from_str_radix(&rest[i + 1..i + 3], 16) {
                out.push(v);
                i += 3;
                continue;
            }
        }
        out.push(b[i]);
        i += 1;
    }
    let s = String::from_utf8(out).ok()?;
    // file:///C:/x -> C:/x
    let s = match s.as_bytes() {
        [b'/', d, b':', ..] if d.is_ascii_alphabetic() => s[1..].to_string(),
        _ => s,
    };
    (!s.is_empty()).then_some(s)
}

fn uuid_at(s: &str) -> Option<usize> {
    let b = s.as_bytes();
    let shape = [8, 4, 4, 4, 12];
    'outer: for start in 0..b.len() {
        let mut i = start;
        for (n, len) in shape.iter().enumerate() {
            for _ in 0..*len {
                if !b.get(i).is_some_and(|c| c.is_ascii_hexdigit()) {
                    continue 'outer;
                }
                i += 1;
            }
            if n < 4 {
                if b.get(i) != Some(&b'-') {
                    continue 'outer;
                }
                i += 1;
            }
        }
        return Some(start);
    }
    None
}

/// …<uuid>/images/<file> -> (uuid, file) (Go reSessionImage).
fn session_image(r: &str) -> Option<(String, String)> {
    let start = uuid_at(r)?;
    let uuid = &r[start..start + 36];
    let rest = r[start + 36..].trim_start_matches(['\\', '/']);
    let rest = rest.strip_prefix("images").or_else(|| rest.strip_prefix("IMAGES"))?;
    let file = rest.trim_start_matches(['\\', '/']);
    (!file.is_empty() && !file.contains(['\\', '/'])).then(|| (uuid.to_string(), file.to_string()))
}

/// Grok prints `…\.grok\sessions\C:\Users\…\proj\<uuid>\images\1.jpg`; the
/// folder is the workspace path encoded (Go fixGrokSessionDisplayPath).
fn fix_grok_session_path(p: &str) -> String {
    let q = p.replace('/', "\\");
    let marker = ".grok\\sessions\\";
    let Some(i) = q.to_lowercase().find(marker) else { return p.to_string() };
    let (prefix, rest) = q.split_at(i + marker.len());
    let Some((uuid, file)) = session_image(rest) else { return p.to_string() };
    let Some(j) = rest.to_lowercase().find(&uuid.to_lowercase()) else { return p.to_string() };
    let ws = rest[..j].trim_end_matches('\\');
    if ws.is_empty() {
        return p.to_string();
    }
    let enc = ws.replace('\\', "%5C").replace(':', "%3A");
    format!("{prefix}{enc}\\{uuid}\\images\\{file}")
}

fn is_image_name(n: &str) -> bool {
    let n = n.to_ascii_lowercase();
    [".png", ".jpg", ".jpeg", ".gif", ".webp"].iter().any(|e| n.ends_with(e))
}

fn find_under(root: &Path, base: &str, depth: usize) -> Option<PathBuf> {
    let mut stack = vec![(root.to_path_buf(), 0)];
    while let Some((dir, d)) = stack.pop() {
        for e in std::fs::read_dir(&dir).ok()?.flatten() {
            let p = e.path();
            let Ok(ft) = e.file_type() else { continue };
            if ft.is_dir() {
                if d + 1 < depth {
                    stack.push((p, d + 1));
                }
            } else if e.file_name().to_string_lossy().eq_ignore_ascii_case(base) {
                return Some(p);
            }
        }
    }
    None
}

/// Read an image file, at most Go's 12 MiB.
pub fn read_image(path: &Path) -> Option<Vec<u8>> {
    let meta = std::fs::metadata(path).ok()?;
    if !meta.is_file() || meta.len() as usize > MAX_IMAGE_BYTES {
        return None;
    }
    std::fs::read(path).ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sizes_like_go() {
        // 9x18 cells, 100 columns: a 300x100 image keeps its size.
        assert_eq!(inline_cells((300, 100), (9, 18), 100), (34, 6));
        // A 4000x4000 image scales to the 39-row cap, keeping its shape.
        assert_eq!(inline_cells((4000, 4000), (9, 18), 100), (78, 39));
        // A tiny one still takes two rows.
        assert_eq!(inline_cells((10, 10), (9, 18), 100).1, 2);
    }

    #[test]
    fn same_file_moves_and_caps() {
        let mut im = Images::default();
        let mut ch = Changes::default();
        let a = im.add_inline("/tmp/a.png", 100, 4, 2, &mut ch);
        let b = im.add_inline("/tmp/./A.png", 101, 4, 2, &mut ch);
        assert_eq!(ch.dropped, vec![a]);
        assert_ne!(a, b);
        for i in 0..10 {
            im.add_inline(&format!("/tmp/{i}.png"), 200 + i, 4, 2, &mut ch);
        }
        assert_eq!(im.inline.len(), MAX_INLINE);
        assert!(ch.dropped.contains(&b));
    }

    #[test]
    fn placements_follow_marks_and_alt_screen() {
        let mut im = Images::default();
        let mut ch = Changes::default();
        let a = im.add_inline("/a.png", 7, 4, 3, &mut ch);
        let p = im.placements(&[(7, 5, 0)], false).unwrap();
        assert_eq!((p[0].image, p[0].row), (a, 5));
        assert!(im.placements(&[(7, 5, 0)], false).is_none());
        // Scrolled so its bottom row is just off the top: gone.
        assert!(im.placements(&[(7, -3, 0)], false).unwrap().is_empty());
        let k = im.kitty_image(9, 10, 10, &mut ch);
        im.kitty_place(9, KittyPlace { cols: 2, rows: 2, ..Default::default() }, None, (3, 4));
        let p = im.placements(&[], true).unwrap();
        assert_eq!((p[0].image, p[0].row, p[0].col), (k, 4, 3));
        assert!(im.placements(&[], false).unwrap().is_empty());
    }

    #[test]
    fn grok_paths() {
        assert_eq!(
            fix_grok_session_path(r"C:\Users\me\.grok\sessions\C:\Users\me\proj\0123abcd-0000-1111-2222-333344445555\images\1.jpg"),
            r"C:\Users\me\.grok\sessions\C%3A%5CUsers%5Cme%5Cproj\0123abcd-0000-1111-2222-333344445555\images\1.jpg"
        );
        assert_eq!(file_uri_path("file:///tmp/a%20b.png").as_deref(), Some("/tmp/a b.png"));
        assert_eq!(file_uri_path("file:///C:/x.png").as_deref(), Some("C:/x.png"));
    }
}
