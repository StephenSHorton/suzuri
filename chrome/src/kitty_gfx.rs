//! Kitty terminal graphics protocol (APC `ESC _ G … ST`).
//!
//! Spec: https://sw.kovidgoyal.net/kitty/graphics-protocol/
//! Enough for terminal-browser / tode (query, direct+file transmit, place, delete)
//! and Grok pixel previews. Animation and shared-memory are rejected with ENOSYS.

use std::collections::HashMap;
use std::io::Cursor;

use crate::cells::CellGrid;
use base64::Engine as _;

/// Unicode placeholder used with virtual placements (`U=1`).
pub const PLACEHOLDER: char = '\u{10EEEE}';

const MAX_APC: usize = 8 * 1024 * 1024;
const MAX_DECODED: usize = 64 * 1024 * 1024;
const MAX_DIM: u32 = 8192;
const MAX_IMAGES: usize = 64;

/// One decoded RGBA image.
#[derive(Clone, Debug)]
pub struct KittyImage {
    pub id: u32,
    pub number: u32,
    pub width: u32,
    pub height: u32,
    pub rgba: Vec<u8>,
    pub gen: u64,
}

/// A placement of an image on the cell grid (live-buffer coordinates).
#[derive(Clone, Copy, Debug)]
pub struct Placement {
    pub image_id: u32,
    pub placement_id: u32,
    pub col: u16,
    pub row: u16,
    pub cols: u16,
    pub rows: u16,
    pub z: i32,
    pub virtual_place: bool,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
enum Quiet {
    #[default]
    No,
    Ok,
    All,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Medium {
    Direct,
    File,
    TempFile,
    Shared,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Format {
    Rgb,
    Rgba,
    Png,
    Unknown,
}

#[derive(Clone, Debug)]
struct Transmission {
    format: Format,
    medium: Medium,
    width: u32,
    height: u32,
    image_id: u32,
    image_number: u32,
    placement_id: u32,
    zlib: bool,
    more: bool,
}

#[derive(Clone, Debug)]
struct Display {
    image_id: u32,
    image_number: u32,
    placement_id: u32,
    columns: u32,
    rows: u32,
    z: i32,
    cursor_stay: bool,
    virtual_place: bool,
}

#[derive(Clone, Debug)]
struct Partial {
    action: u8,
    quiet: Quiet,
    tx: Transmission,
    display: Display,
    data: Vec<u8>,
}

/// Per-PTY graphics state.
#[derive(Debug)]
pub struct GraphicsStore {
    images: HashMap<u32, KittyImage>,
    by_number: HashMap<u32, u32>,
    placements: Vec<Placement>,
    next_id: u32,
    next_gen: u64,
    partial: Option<Partial>,
    /// Physical cell size (CSI 16 t).
    pub cell_px: (u32, u32),
    /// Physical text-area size (CSI 14 t).
    pub area_px: (u32, u32),
    /// Character grid (CSI 18 t).
    pub chars: (u32, u32),
}

impl Default for GraphicsStore {
    fn default() -> Self {
        Self {
            images: HashMap::new(),
            by_number: HashMap::new(),
            placements: Vec::new(),
            next_id: 1,
            next_gen: 1,
            partial: None,
            cell_px: (8, 16),
            area_px: (640, 384),
            chars: (80, 24),
        }
    }
}

impl GraphicsStore {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn set_pixel_metrics(
        &mut self,
        cell_w: u32,
        cell_h: u32,
        area_w: u32,
        area_h: u32,
        cols: u32,
        rows: u32,
    ) {
        self.cell_px = (cell_w.max(1), cell_h.max(1));
        self.area_px = (area_w.max(1), area_h.max(1));
        self.chars = (cols.max(1), rows.max(1));
    }

    pub fn images(&self) -> impl Iterator<Item = &KittyImage> {
        self.images.values()
    }

    pub fn placements(&self) -> &[Placement] {
        &self.placements
    }

    pub fn image(&self, id: u32) -> Option<&KittyImage> {
        self.images.get(&id)
    }

    pub fn has_images(&self) -> bool {
        !self.images.is_empty() || !self.placements.is_empty()
    }

    pub fn clear_placements(&mut self) {
        self.placements.clear();
    }

    pub fn clear_all(&mut self) {
        self.images.clear();
        self.by_number.clear();
        self.placements.clear();
        self.partial = None;
    }

    /// Handle one APC payload starting *after* the leading `G`.
    pub fn execute(&mut self, payload: &[u8], grid: &mut CellGrid) -> Vec<Vec<u8>> {
        if payload.len() > MAX_APC {
            return Vec::new();
        }
        let (kv, data) = match parse_payload(payload) {
            Some(v) => v,
            None => return Vec::new(),
        };
        let action = kv_char(&kv, b'a').unwrap_or(b't');
        let quiet = match kv_u32(&kv, b'q').unwrap_or(0) {
            0 => Quiet::No,
            1 => Quiet::Ok,
            _ => Quiet::All,
        };
        let more = kv_u32(&kv, b'm').unwrap_or(0) > 0;

        // Continuation chunks (usually only `m` + data).
        if self.partial.is_some() && !kv.contains_key(&b'a') && !kv.contains_key(&b'i') {
            return self.continue_chunk(data, more, grid);
        }

        match action {
            b'q' => self.query(&kv, data, quiet),
            b't' | b'T' => self.transmit(&kv, data, action == b'T', more, quiet, grid),
            b'p' => self.display_only(&kv, quiet, grid),
            b'd' => self.delete(&kv, quiet),
            _ => {
                // Animation / unknown: ignore unless we must error.
                if quiet == Quiet::No {
                    if let Some(id) = kv_u32(&kv, b'i').filter(|v| *v > 0) {
                        return vec![gfx_reply(id, kv_u32(&kv, b'I').unwrap_or(0), "ENOSYS")];
                    }
                }
                Vec::new()
            }
        }
    }

    fn continue_chunk(&mut self, data: &[u8], more: bool, grid: &mut CellGrid) -> Vec<Vec<u8>> {
        let mut part = match self.partial.take() {
            Some(p) => p,
            None => return Vec::new(),
        };
        if part.data.len().saturating_add(data.len()) > MAX_APC {
            return self.maybe_err(part.tx.image_id, part.tx.image_number, part.quiet, "EFBIG");
        }
        part.data.extend_from_slice(data);
        if more {
            self.partial = Some(part);
            return Vec::new();
        }
        self.finish_transmit(part, grid)
    }

    fn query(&mut self, kv: &HashMap<u8, i64>, _data: &[u8], _quiet: Quiet) -> Vec<Vec<u8>> {
        let id = kv_u32(kv, b'i').unwrap_or(0);
        let num = kv_u32(kv, b'I').unwrap_or(0);
        let medium = parse_medium(kv);
        let msg = match medium {
            Medium::Direct | Medium::File | Medium::TempFile => "OK",
            Medium::Shared => "ENOSYS: shared memory not supported",
        };
        // Query always replies — that is the whole point of a=q.
        if id == 0 && num == 0 {
            return Vec::new();
        }
        vec![gfx_reply(id, num, msg)]
    }

    fn transmit(
        &mut self,
        kv: &HashMap<u8, i64>,
        data: &[u8],
        and_display: bool,
        more: bool,
        quiet: Quiet,
        grid: &mut CellGrid,
    ) -> Vec<Vec<u8>> {
        let tx = parse_tx(kv);
        let display = parse_display(kv);
        let mut part = Partial {
            action: if and_display { b'T' } else { b't' },
            quiet,
            tx,
            display,
            data: data.to_vec(),
        };
        if more {
            self.partial = Some(part);
            return Vec::new();
        }
        // Direct medium is the only one that chunks; local media ignore m.
        if part.tx.medium != Medium::Direct {
            part.tx.more = false;
        }
        self.finish_transmit(part, grid)
    }

    fn finish_transmit(&mut self, part: Partial, grid: &mut CellGrid) -> Vec<Vec<u8>> {
        let Partial {
            action,
            quiet,
            mut tx,
            mut display,
            data,
        } = part;
        if tx.medium == Medium::Shared {
            return self.maybe_err(tx.image_id, tx.image_number, quiet, "ENOSYS");
        }
        let decoded = match decode_b64(&data) {
            Ok(d) => d,
            Err(_) => {
                return self.maybe_err(tx.image_id, tx.image_number, quiet, "EINVAL: bad base64")
            }
        };
        let pixels = match load_pixels(&tx, &decoded) {
            Ok(p) => p,
            Err(e) => return self.maybe_err(tx.image_id, tx.image_number, quiet, e),
        };
        if tx.image_id == 0 {
            tx.image_id = self.alloc_id();
        }
        if tx.image_number > 0 {
            self.by_number.insert(tx.image_number, tx.image_id);
        }
        self.store_image(tx.image_id, tx.image_number, pixels.0, pixels.1, pixels.2);
        display.image_id = tx.image_id;
        display.image_number = tx.image_number;
        display.placement_id = if display.placement_id == 0 {
            tx.placement_id
        } else {
            display.placement_id
        };
        let mut replies = Vec::new();
        if tx.image_number > 0 && quiet != Quiet::All {
            // Must report the assigned id so the client can address it.
            replies.push(gfx_reply(tx.image_id, tx.image_number, "OK"));
        } else if quiet == Quiet::No {
            replies.push(gfx_reply(tx.image_id, tx.image_number, "OK"));
        }
        if action == b'T' {
            self.place(&display, grid);
        }
        replies
    }

    fn display_only(
        &mut self,
        kv: &HashMap<u8, i64>,
        quiet: Quiet,
        grid: &mut CellGrid,
    ) -> Vec<Vec<u8>> {
        let mut d = parse_display(kv);
        if d.image_id == 0 {
            if let Some(n) = kv_u32(kv, b'I').filter(|v| *v > 0) {
                if let Some(&id) = self.by_number.get(&n) {
                    d.image_id = id;
                }
            }
        }
        if d.image_id == 0 || !self.images.contains_key(&d.image_id) {
            return self.maybe_err(d.image_id, d.image_number, quiet, "ENOENT");
        }
        self.place(&d, grid);
        if quiet == Quiet::No {
            vec![gfx_reply(d.image_id, d.image_number, "OK")]
        } else {
            Vec::new()
        }
    }

    fn place(&mut self, d: &Display, grid: &mut CellGrid) {
        let Some(img) = self.images.get(&d.image_id) else {
            return;
        };
        let cell_w = self.cell_px.0.max(1);
        let cell_h = self.cell_px.1.max(1);
        let cols = if d.columns > 0 {
            d.columns
        } else {
            img.width.div_ceil(cell_w).max(1)
        };
        let rows = if d.rows > 0 {
            d.rows
        } else {
            img.height.div_ceil(cell_h).max(1)
        };
        let cur = grid.cursor();
        let (cols, rows) = if d.virtual_place {
            (cols, rows)
        } else {
            (
                cols.min((grid.cols().saturating_sub(cur.col) as u32).max(1)),
                rows.min((grid.rows().saturating_sub(cur.row) as u32).max(1)),
            )
        };
        let p = Placement {
            image_id: d.image_id,
            placement_id: d.placement_id,
            col: cur.col,
            row: cur.row,
            cols: cols.min(u16::MAX as u32) as u16,
            rows: rows.min(u16::MAX as u32) as u16,
            z: d.z,
            virtual_place: d.virtual_place,
        };
        // Replace same (image, placement) pair.
        self.placements.retain(|q| {
            !(q.image_id == p.image_id && (p.placement_id == 0 || q.placement_id == p.placement_id))
        });
        self.placements.push(p);
        if !d.virtual_place && !d.cursor_stay {
            let next_col = cur.col.saturating_add(p.cols);
            if next_col >= grid.cols() {
                let next_row = cur
                    .row
                    .saturating_add(p.rows)
                    .min(grid.rows().saturating_sub(1));
                grid.set_cursor(0, next_row);
            } else {
                grid.set_cursor(next_col, cur.row.saturating_add(p.rows.saturating_sub(1)));
            }
        }
    }

    fn delete(&mut self, kv: &HashMap<u8, i64>, _quiet: Quiet) -> Vec<Vec<u8>> {
        let what = kv_char(kv, b'd').unwrap_or(b'a');
        let free_data = what.is_ascii_uppercase();
        let kind = what.to_ascii_lowercase();
        match kind {
            b'a' => {
                self.placements.clear();
                if free_data {
                    self.images.clear();
                    self.by_number.clear();
                }
            }
            b'i' => {
                let id = kv_u32(kv, b'i').unwrap_or(0);
                let pid = kv_u32(kv, b'p').unwrap_or(0);
                self.placements.retain(|p| {
                    if p.image_id != id {
                        return true;
                    }
                    if pid != 0 && p.placement_id != pid as u32 {
                        return true;
                    }
                    false
                });
                if free_data {
                    self.images.remove(&id);
                    self.by_number.retain(|_, v| *v != id);
                }
            }
            b'n' => {
                let num = kv_u32(kv, b'I').unwrap_or(0);
                if let Some(&id) = self.by_number.get(&num) {
                    self.placements.retain(|p| p.image_id != id);
                    if free_data {
                        self.images.remove(&id);
                        self.by_number.remove(&num);
                    }
                }
            }
            _ => {}
        }
        Vec::new()
    }

    fn store_image(&mut self, id: u32, number: u32, width: u32, height: u32, rgba: Vec<u8>) {
        let gen = self.next_gen;
        self.next_gen = self.next_gen.wrapping_add(1).max(1);
        if let Some(old) = self.images.get_mut(&id) {
            old.number = number;
            old.width = width;
            old.height = height;
            old.rgba = rgba;
            old.gen = gen;
        } else {
            if self.images.len() >= MAX_IMAGES {
                if let Some((&victim, _)) = self.images.iter().next() {
                    self.images.remove(&victim);
                    self.placements.retain(|p| p.image_id != victim);
                    self.by_number.retain(|_, v| *v != victim);
                }
            }
            self.images.insert(
                id,
                KittyImage {
                    id,
                    number,
                    width,
                    height,
                    rgba,
                    gen,
                },
            );
        }
    }

    fn alloc_id(&mut self) -> u32 {
        for _ in 0..u32::MAX {
            let id = self.next_id;
            self.next_id = self.next_id.wrapping_add(1).max(1);
            if !self.images.contains_key(&id) {
                return id;
            }
        }
        1
    }

    fn maybe_err(&self, id: u32, num: u32, quiet: Quiet, msg: &str) -> Vec<Vec<u8>> {
        if quiet == Quiet::All {
            return Vec::new();
        }
        if id == 0 && num == 0 {
            return Vec::new();
        }
        vec![gfx_reply(id, num, msg)]
    }
}

fn gfx_reply(id: u32, num: u32, msg: &str) -> Vec<u8> {
    let mut s = String::from("\x1b_G");
    let mut prior = false;
    if id > 0 {
        s.push_str(&format!("i={id}"));
        prior = true;
    }
    if num > 0 {
        if prior {
            s.push(',');
        }
        s.push_str(&format!("I={num}"));
    }
    s.push(';');
    s.push_str(msg);
    s.push_str("\x1b\\");
    s.into_bytes()
}

fn parse_payload(body: &[u8]) -> Option<(HashMap<u8, i64>, &[u8])> {
    let (ctrl, data) = match body.iter().position(|&b| b == b';') {
        Some(i) => (&body[..i], &body[i + 1..]),
        None => (body, &body[body.len()..]),
    };
    let mut kv = HashMap::new();
    if !ctrl.is_empty() {
        for pair in ctrl.split(|&b| b == b',') {
            if pair.is_empty() {
                continue;
            }
            let eq = pair.iter().position(|&b| b == b'=')?;
            if eq != 1 {
                continue; // ignore unknown long keys
            }
            let key = pair[0];
            let val = &pair[eq + 1..];
            kv.insert(key, parse_val(val));
        }
    }
    Some((kv, data))
}

fn parse_val(s: &[u8]) -> i64 {
    if s.len() == 1 && !s[0].is_ascii_digit() && s[0] != b'-' {
        return s[0] as i64;
    }
    std::str::from_utf8(s)
        .ok()
        .and_then(|t| t.parse::<i64>().ok())
        .unwrap_or(0)
}

fn kv_u32(kv: &HashMap<u8, i64>, k: u8) -> Option<u32> {
    kv.get(&k).copied().and_then(|v| u32::try_from(v).ok())
}

fn kv_i32(kv: &HashMap<u8, i64>, k: u8) -> Option<i32> {
    kv.get(&k).copied().and_then(|v| i32::try_from(v).ok())
}

fn kv_char(kv: &HashMap<u8, i64>, k: u8) -> Option<u8> {
    kv.get(&k).copied().and_then(|v| u8::try_from(v).ok())
}

fn parse_medium(kv: &HashMap<u8, i64>) -> Medium {
    match kv_char(kv, b't').unwrap_or(b'd') {
        b'f' => Medium::File,
        b't' => Medium::TempFile,
        b's' => Medium::Shared,
        _ => Medium::Direct,
    }
}

fn parse_format(kv: &HashMap<u8, i64>) -> Format {
    match kv_u32(kv, b'f').unwrap_or(32) {
        24 => Format::Rgb,
        0 | 32 => Format::Rgba,
        100 => Format::Png,
        _ => Format::Unknown,
    }
}

fn parse_tx(kv: &HashMap<u8, i64>) -> Transmission {
    Transmission {
        format: parse_format(kv),
        medium: parse_medium(kv),
        width: kv_u32(kv, b's').unwrap_or(0),
        height: kv_u32(kv, b'v').unwrap_or(0),
        image_id: kv_u32(kv, b'i').unwrap_or(0),
        image_number: kv_u32(kv, b'I').unwrap_or(0),
        placement_id: kv_u32(kv, b'p').unwrap_or(0),
        zlib: kv_char(kv, b'o') == Some(b'z'),
        more: kv_u32(kv, b'm').unwrap_or(0) > 0 && parse_medium(kv) == Medium::Direct,
    }
}

fn parse_display(kv: &HashMap<u8, i64>) -> Display {
    Display {
        image_id: kv_u32(kv, b'i').unwrap_or(0),
        image_number: kv_u32(kv, b'I').unwrap_or(0),
        placement_id: kv_u32(kv, b'p').unwrap_or(0),
        columns: kv_u32(kv, b'c').unwrap_or(0),
        rows: kv_u32(kv, b'r').unwrap_or(0),
        z: kv_i32(kv, b'z').unwrap_or(0),
        cursor_stay: kv_u32(kv, b'C').unwrap_or(0) == 1,
        virtual_place: kv_u32(kv, b'U').unwrap_or(0) != 0,
    }
}

fn decode_b64(data: &[u8]) -> Result<Vec<u8>, ()> {
    let cleaned: Vec<u8> = data
        .iter()
        .copied()
        .filter(|b| !b.is_ascii_whitespace())
        .collect();
    if cleaned.is_empty() {
        return Ok(Vec::new());
    }
    let engine = base64::engine::general_purpose::STANDARD;
    engine.decode(&cleaned).or_else(|_| {
        base64::engine::general_purpose::STANDARD_NO_PAD
            .decode(&cleaned)
            .map_err(|_| ())
    })
}

fn load_pixels(tx: &Transmission, decoded: &[u8]) -> Result<(u32, u32, Vec<u8>), &'static str> {
    let raw = match tx.medium {
        Medium::Direct => decoded.to_vec(),
        Medium::File | Medium::TempFile => {
            let path = std::str::from_utf8(decoded).map_err(|_| "EINVAL: path")?;
            if path.is_empty() || path.contains('\0') {
                return Err("EINVAL: path");
            }
            let bytes = std::fs::read(path).map_err(|_| "ENOENT")?;
            if tx.medium == Medium::TempFile {
                let _ = std::fs::remove_file(path);
            }
            bytes
        }
        Medium::Shared => return Err("ENOSYS"),
    };
    let raw = if tx.zlib {
        miniz_oxide::inflate::decompress_to_vec_zlib_with_limit(&raw, MAX_DECODED)
            .map_err(|_| "EINVAL: zlib")?
    } else {
        raw
    };
    if raw.len() > MAX_DECODED {
        return Err("EFBIG");
    }
    match tx.format {
        Format::Unknown => Err("EINVAL: format"),
        Format::Png => decode_png(&raw),
        Format::Rgb => {
            let (w, h) = (tx.width, tx.height);
            if w == 0 || h == 0 || w > MAX_DIM || h > MAX_DIM {
                return Err("EINVAL: size");
            }
            let need = (w as usize).saturating_mul(h as usize).saturating_mul(3);
            if raw.len() < need {
                return Err("EINVAL: size");
            }
            let mut rgba = Vec::with_capacity(need / 3 * 4);
            for px in raw[..need].chunks_exact(3) {
                rgba.extend_from_slice(&[px[0], px[1], px[2], 255]);
            }
            Ok((w, h, rgba))
        }
        Format::Rgba => {
            let (w, h) = (tx.width, tx.height);
            if w == 0 || h == 0 || w > MAX_DIM || h > MAX_DIM {
                return Err("EINVAL: size");
            }
            let need = (w as usize).saturating_mul(h as usize).saturating_mul(4);
            if raw.len() < need {
                return Err("EINVAL: size");
            }
            Ok((w, h, raw[..need].to_vec()))
        }
    }
}

fn decode_png(data: &[u8]) -> Result<(u32, u32, Vec<u8>), &'static str> {
    let mut decoder = png::Decoder::new(Cursor::new(data));
    decoder.set_transformations(png::Transformations::EXPAND | png::Transformations::ALPHA);
    let mut reader = decoder.read_info().map_err(|_| "EINVAL: png")?;
    let mut buf = vec![0u8; reader.output_buffer_size()];
    let info = reader.next_frame(&mut buf).map_err(|_| "EINVAL: png")?;
    let w = info.width;
    let h = info.height;
    if w > MAX_DIM || h > MAX_DIM {
        return Err("EFBIG");
    }
    let rgba = match info.color_type {
        png::ColorType::Rgba => buf[..info.buffer_size()].to_vec(),
        png::ColorType::Rgb => {
            let src = &buf[..info.buffer_size()];
            let mut out = Vec::with_capacity(src.len() / 3 * 4);
            for px in src.chunks_exact(3) {
                out.extend_from_slice(&[px[0], px[1], px[2], 255]);
            }
            out
        }
        png::ColorType::GrayscaleAlpha => {
            let src = &buf[..info.buffer_size()];
            let mut out = Vec::with_capacity(src.len() / 2 * 4);
            for px in src.chunks_exact(2) {
                out.extend_from_slice(&[px[0], px[0], px[0], px[1]]);
            }
            out
        }
        png::ColorType::Grayscale => {
            let src = &buf[..info.buffer_size()];
            let mut out = Vec::with_capacity(src.len() * 4);
            for &g in src {
                out.extend_from_slice(&[g, g, g, 255]);
            }
            out
        }
        _ => return Err("EINVAL: png"),
    };
    Ok((w, h, rgba))
}

/// Image id encoded in a placeholder cell's 24-bit foreground.
pub fn placeholder_id(fg: [f32; 3]) -> u32 {
    let r = (fg[0] * 255.0).round().clamp(0.0, 255.0) as u32;
    let g = (fg[1] * 255.0).round().clamp(0.0, 255.0) as u32;
    let b = (fg[2] * 255.0).round().clamp(0.0, 255.0) as u32;
    (r << 16) | (g << 8) | b
}

/// Bounding boxes of placeholder cells on the live grid: id → (col, row, cols, rows).
pub fn placeholder_bounds(grid: &CellGrid) -> HashMap<u32, (u16, u16, u16, u16)> {
    let mut acc: HashMap<u32, (u16, u16, u16, u16)> = HashMap::new();
    for row in 0..grid.rows() {
        let cells = grid.live_row_cells(row);
        for (col, c) in cells.iter().enumerate() {
            if c.ch != PLACEHOLDER {
                continue;
            }
            let id = placeholder_id(c.fg);
            if id == 0 {
                continue;
            }
            let col = col as u16;
            acc.entry(id)
                .and_modify(|(c0, r0, c1, r1)| {
                    *c0 = (*c0).min(col);
                    *r0 = (*r0).min(row);
                    *c1 = (*c1).max(col);
                    *r1 = (*r1).max(row);
                })
                .or_insert((col, row, col, row));
        }
    }
    acc.into_iter()
        .map(|(id, (c0, r0, c1, r1))| {
            (
                id,
                (c0, r0, c1.saturating_sub(c0) + 1, r1.saturating_sub(r0) + 1),
            )
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::cells::CellGrid;

    fn feed_apc(store: &mut GraphicsStore, grid: &mut CellGrid, body: &str) -> Vec<Vec<u8>> {
        // body is the inside of ESC _ G … ST (no G prefix).
        store.execute(body.as_bytes(), grid)
    }

    #[test]
    fn query_direct_replies_ok() {
        let mut g = GraphicsStore::new();
        let mut grid = CellGrid::new(40, 10);
        let replies = feed_apc(&mut g, &mut grid, "i=4207,a=q,t=d,f=24,s=1,v=1;AAAA");
        assert_eq!(replies.len(), 1);
        let s = String::from_utf8(replies[0].clone()).unwrap();
        assert!(s.contains("Gi=4207;OK"), "{s}");
        assert!(g.images.is_empty());
    }

    #[test]
    fn query_shared_memory_is_enosys() {
        let mut g = GraphicsStore::new();
        let mut grid = CellGrid::new(40, 10);
        let replies = feed_apc(&mut g, &mut grid, "i=3,a=q,t=s,f=32,s=1,v=1;L3g=");
        let s = String::from_utf8(replies[0].clone()).unwrap();
        assert!(s.contains("ENOSYS"), "{s}");
    }

    #[test]
    fn transmit_rgba_and_place_at_cursor() {
        let mut g = GraphicsStore::new();
        g.cell_px = (8, 16);
        let mut grid = CellGrid::new(40, 10);
        let rgba = [255u8, 0, 0, 255];
        let b64 = base64::engine::general_purpose::STANDARD.encode(rgba);
        let body = format!("a=T,f=32,s=1,v=1,t=d,i=7,p=1,C=1,q=2,m=0;{b64}");
        let replies = feed_apc(&mut g, &mut grid, &body);
        assert!(replies.is_empty(), "q=2 suppresses OK");
        let img = g.image(7).expect("stored");
        assert_eq!(img.width, 1);
        assert_eq!(img.rgba, rgba);
        assert_eq!(g.placements.len(), 1);
        assert_eq!(g.placements[0].image_id, 7);
        assert_eq!(g.placements[0].cols, 1);
        assert_eq!(grid.cursor().col, 0, "C=1 must not move the cursor");
    }

    #[test]
    fn zlib_direct_roundtrip() {
        let mut g = GraphicsStore::new();
        let mut grid = CellGrid::new(20, 5);
        let rgba = [1u8, 2, 3, 4];
        let z = miniz_oxide::deflate::compress_to_vec_zlib(&rgba, 1);
        let b64 = base64::engine::general_purpose::STANDARD.encode(z);
        feed_apc(
            &mut g,
            &mut grid,
            &format!("a=t,f=32,o=z,s=1,v=1,t=d,i=9,q=2;{b64}"),
        );
        assert_eq!(g.image(9).unwrap().rgba, rgba);
    }

    #[test]
    fn chunked_payload_assembles() {
        let mut g = GraphicsStore::new();
        let mut grid = CellGrid::new(20, 5);
        let rgba = [9u8, 8, 7, 6];
        let b64 = base64::engine::general_purpose::STANDARD.encode(rgba);
        let (a, b) = b64.split_at(2);
        feed_apc(
            &mut g,
            &mut grid,
            &format!("a=t,f=32,s=1,v=1,t=d,i=4,q=2,m=1;{a}"),
        );
        assert!(g.image(4).is_none());
        feed_apc(&mut g, &mut grid, &format!("m=0;{b}"));
        assert_eq!(g.image(4).unwrap().rgba, rgba);
    }

    #[test]
    fn delete_all_frees_data() {
        let mut g = GraphicsStore::new();
        let mut grid = CellGrid::new(20, 5);
        let b64 = base64::engine::general_purpose::STANDARD.encode([1u8, 2, 3, 4]);
        feed_apc(
            &mut g,
            &mut grid,
            &format!("a=T,f=32,s=1,v=1,t=d,i=1,C=1,q=2;{b64}"),
        );
        assert!(!g.images.is_empty());
        feed_apc(&mut g, &mut grid, "a=d,d=A,q=2");
        assert!(g.images.is_empty());
        assert!(g.placements.is_empty());
    }

    #[test]
    fn probe_sequence_then_da_is_client_side() {
        // Integration lives in ansi tests; here we just match the needle.
        let mut g = GraphicsStore::new();
        let mut grid = CellGrid::new(40, 5);
        let replies = feed_apc(&mut g, &mut grid, "i=4207,a=q,t=d,f=24,s=1,v=1;AAAA");
        let s = String::from_utf8_lossy(&replies[0]);
        let at = s.find("Gi=4207;").unwrap();
        assert!(s[at + "Gi=4207;".len()..].starts_with("OK"));
    }
}
