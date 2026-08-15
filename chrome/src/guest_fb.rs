//! File-backed BGRA framebuffer a guest process paints and chrome uploads.
//!
//! Layout (little-endian):
//!   0  magic `SZFB`
//!   4  u32 width
//!   8  u32 height
//!  12  u32 seq
//!  16  width * height * 4 bytes BGRA, row-major, top-left origin.

use std::fs::{self, File, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::path::{Path, PathBuf};

pub const MAGIC: &[u8; 4] = b"SZFB";
pub const HEADER: usize = 16;

pub fn fb_path(pane_id: u64) -> PathBuf {
    std::env::temp_dir().join(format!(
        "suzuri-guest-fb-{}-{pane_id}.szfb",
        std::process::id()
    ))
}

pub fn pixel_size(w_logical: f32, h_logical: f32, scale: f32) -> (u32, u32) {
    let s = scale.max(0.5);
    let w = (w_logical * s).round().clamp(1.0, 4096.0) as u32;
    let h = (h_logical * s).round().clamp(1.0, 4096.0) as u32;
    (w, h)
}

pub fn create(path: &Path, w: u32, h: u32) -> Result<(), String> {
    if let Some(dir) = path.parent() {
        let _ = fs::create_dir_all(dir);
    }
    let bytes = HEADER + (w as usize) * (h as usize) * 4;
    // Never truncate an existing well — Ladybird may have it mmap'd.
    if let Ok(meta) = fs::metadata(path) {
        if meta.len() == bytes as u64 {
            return Ok(());
        }
    }
    let mut f = OpenOptions::new()
        .create(true)
        .write(true)
        .read(true)
        .open(path)
        .map_err(|e| e.to_string())?;
    f.set_len(bytes as u64).map_err(|e| e.to_string())?;
    write_header(&mut f, w, h, 0)?;
    Ok(())
}

pub fn remove(path: &Path) {
    let _ = fs::remove_file(path);
}

/// Pixel payload first, then header — so a reader that sees a new `seq` has complete rows.
pub fn write_pixels(path: &Path, w: u32, h: u32, seq: u32, bgra: &[u8]) -> Result<(), String> {
    let n = (w as usize).saturating_mul(h as usize).saturating_mul(4);
    if w == 0 || h == 0 || bgra.len() < n {
        return Err("short pixels".into());
    }
    if let Some(dir) = path.parent() {
        let _ = fs::create_dir_all(dir);
    }
    let mut f = OpenOptions::new()
        .create(true)
        .write(true)
        .read(true)
        .open(path)
        .map_err(|e| e.to_string())?;
    f.set_len((HEADER + n) as u64).map_err(|e| e.to_string())?;
    f.seek(SeekFrom::Start(HEADER as u64))
        .map_err(|e| e.to_string())?;
    f.write_all(&bgra[..n]).map_err(|e| e.to_string())?;
    write_header(&mut f, w, h, seq)?;
    f.flush().map_err(|e| e.to_string())?;
    Ok(())
}

pub fn peek_seq(path: &Path) -> Option<u32> {
    let mut f = File::open(path).ok()?;
    let mut hdr = [0u8; HEADER];
    f.read_exact(&mut hdr).ok()?;
    if &hdr[0..4] != MAGIC {
        return None;
    }
    Some(u32::from_le_bytes(hdr[12..16].try_into().ok()?))
}

/// Read pixels if `seq` is newer than `last_seq`.
pub fn read_if_newer(
    path: &Path,
    last_seq: u32,
) -> Result<Option<(u32, u32, u32, Vec<u8>)>, String> {
    let mut f = File::open(path).map_err(|e| e.to_string())?;
    let mut hdr = [0u8; HEADER];
    f.read_exact(&mut hdr).map_err(|e| e.to_string())?;
    if &hdr[0..4] != MAGIC {
        return Ok(None);
    }
    let w = u32::from_le_bytes(hdr[4..8].try_into().unwrap());
    let h = u32::from_le_bytes(hdr[8..12].try_into().unwrap());
    let seq = u32::from_le_bytes(hdr[12..16].try_into().unwrap());
    if w == 0 || h == 0 || w > 4096 || h > 4096 {
        return Ok(None);
    }
    if seq == last_seq {
        return Ok(None);
    }
    let n = (w as usize) * (h as usize) * 4;
    let mut px = vec![0u8; n];
    f.read_exact(&mut px).map_err(|e| e.to_string())?;
    Ok(Some((w, h, seq, px)))
}

fn write_header(f: &mut File, w: u32, h: u32, seq: u32) -> Result<(), String> {
    f.seek(SeekFrom::Start(0)).map_err(|e| e.to_string())?;
    let mut hdr = [0u8; HEADER];
    hdr[0..4].copy_from_slice(MAGIC);
    hdr[4..8].copy_from_slice(&w.to_le_bytes());
    hdr[8..12].copy_from_slice(&h.to_le_bytes());
    hdr[12..16].copy_from_slice(&seq.to_le_bytes());
    f.write_all(&hdr).map_err(|e| e.to_string())?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn create_and_read() {
        let p = std::env::temp_dir().join(format!("suzuri-fb-test-{}", std::process::id()));
        create(&p, 4, 2).unwrap();
        assert!(read_if_newer(&p, 0).unwrap().is_none());
        assert_eq!(peek_seq(&p), Some(0));
        let mut px = vec![0u8; 4 * 2 * 4];
        px[0] = 80;
        px[1] = 160;
        px[2] = 60;
        px[3] = 255;
        write_pixels(&p, 4, 2, 1, &px).unwrap();
        let (w, h, seq, got) = read_if_newer(&p, 0).unwrap().unwrap();
        assert_eq!((w, h, seq), (4, 2, 1));
        assert_eq!(&got[0..4], &[80, 160, 60, 255]);
        assert!(read_if_newer(&p, 1).unwrap().is_none());
        create(&p, 4, 2).unwrap();
        assert_eq!(peek_seq(&p), Some(1), "same-size create must not wipe mmap");
        let _ = fs::remove_file(&p);
    }
}
