//! Custom chrome backdrop — still image, animated GIF/WebP, file or http(s) URL.
//!
//! Glass still samples the rain RT; wallpaper is composited under (additive)
//! glyph rain so a photo can sit behind the existing optical stack.
//!
//! YouTube / other web players are out of scope: this process has no webview.

use std::fs;
use std::io::Cursor;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::Arc;
use std::time::Duration;

use sha2::{Digest, Sha256};

use crate::config_store;

/// Longest edge after decode (GPU upload + cover-fit).
pub const MAX_EDGE: u32 = 2048;
/// Download / file cap before decode.
pub const MAX_BYTES: u64 = 24 * 1024 * 1024;
/// Animated frame budget (looping GIF/WebP).
pub const MAX_FRAMES: usize = 180;
pub const FETCH_TIMEOUT: Duration = Duration::from_secs(20);

/// One GPU-ready frame (RGBA8, top-left).
#[derive(Clone, Debug)]
pub struct WallpaperFrame {
    pub rgba: Arc<[u8]>,
    pub w: u32,
    pub h: u32,
    /// Seconds to hold this frame. Zero for stills.
    pub delay: f32,
}

/// Decoded backdrop ready for the renderer.
#[derive(Clone, Debug)]
pub struct WallpaperAsset {
    /// User-facing source (path or URL) as stored in prefs.
    pub source: String,
    pub animated: bool,
    pub frames: Arc<[WallpaperFrame]>,
}

impl WallpaperAsset {
    pub fn first(&self) -> Option<&WallpaperFrame> {
        self.frames.first()
    }
}

/// Why this source cannot be a wallpaper (YouTube, video container, …).
pub fn reject_reason(src: &str) -> Option<&'static str> {
    let s = src.trim();
    if s.is_empty() {
        return Some("empty");
    }
    if is_youtube(s) || is_vimeo(s) {
        return Some(
            "YouTube/Vimeo links need a webview — suzuri hasn't got one. Use a file or a direct image URL.",
        );
    }
    if looks_like_video(s) {
        return Some(
            "Video files aren't a backdrop yet. Export a looping GIF/WebP, or pick a still.",
        );
    }
    None
}

pub fn is_http_url(src: &str) -> bool {
    let s = src.trim();
    let lower = s.to_ascii_lowercase();
    lower.starts_with("https://") || lower.starts_with("http://")
}

pub fn looks_like_video(src: &str) -> bool {
    matches!(
        ext_lower(src).as_deref(),
        Some("mp4" | "mov" | "m4v" | "webm" | "mkv" | "avi" | "ogv")
    )
}

fn is_youtube(src: &str) -> bool {
    let l = src.to_ascii_lowercase();
    l.contains("youtube.com/")
        || l.contains("youtu.be/")
        || l.contains("youtube-nocookie.com/")
}

fn is_vimeo(src: &str) -> bool {
    src.to_ascii_lowercase().contains("vimeo.com/")
}

/// Short settings/toast label.
pub fn display_label(src: &str) -> String {
    let s = src.trim();
    if s.is_empty() {
        return "none".into();
    }
    if is_http_url(s) {
        let rest = s
            .split_once("://")
            .map(|(_, r)| r)
            .unwrap_or(s);
        let host = rest.split('/').next().unwrap_or(rest);
        return ellipsize(host, 22);
    }
    let name = Path::new(s)
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or(s);
    ellipsize(name, 22)
}

fn ellipsize(s: &str, max: usize) -> String {
    let n = s.chars().count();
    if n <= max {
        return s.to_string();
    }
    let keep = max.saturating_sub(1);
    let mut out: String = s.chars().take(keep).collect();
    out.push('…');
    out
}

fn ext_lower(path: &str) -> Option<String> {
    Path::new(path.split('?').next().unwrap_or(path))
        .extension()
        .and_then(|e| e.to_str())
        .map(|e| e.trim_start_matches('.').to_ascii_lowercase())
}

/// Cache file for a source string (`…/suzuri/wallpaper/<sha256>`).
pub fn cache_path(source: &str) -> PathBuf {
    let mut h = Sha256::new();
    h.update(source.trim().as_bytes());
    let hex = h
        .finalize()
        .iter()
        .take(16)
        .map(|b| format!("{b:02x}"))
        .collect::<String>();
    config_store::product_config_dir()
        .join("wallpaper")
        .join(hex)
}

/// Copy a local file into the wallpaper cache. Returns the cache path.
pub fn cache_local_file(src: &Path) -> Result<PathBuf, String> {
    let meta = fs::metadata(src).map_err(|e| format!("couldn't read file: {e}"))?;
    if meta.len() > MAX_BYTES {
        return Err("image is too large (24 MB max)".into());
    }
    let dest = cache_path(&src.to_string_lossy());
    if let Some(dir) = dest.parent() {
        fs::create_dir_all(dir).map_err(|e| e.to_string())?;
    }
    fs::copy(src, &dest).map_err(|e| format!("couldn't copy wallpaper: {e}"))?;
    Ok(dest)
}

/// Fetch http(s) into the cache. Returns cache path.
pub fn cache_url(url: &str) -> Result<PathBuf, String> {
    if let Some(why) = reject_reason(url) {
        return Err(why.into());
    }
    if !is_http_url(url) {
        return Err("wallpaper URL must be http or https".into());
    }
    let dest = cache_path(url);
    if dest.is_file() {
        if let Ok(meta) = fs::metadata(&dest) {
            if meta.len() > 0 && meta.len() <= MAX_BYTES {
                return Ok(dest);
            }
        }
    }
    if let Some(dir) = dest.parent() {
        fs::create_dir_all(dir).map_err(|e| e.to_string())?;
    }
    let tmp = dest.with_extension("download");
    let secs = FETCH_TIMEOUT.as_secs().to_string();
    let max = MAX_BYTES.to_string();
    let status = Command::new("curl")
        .args([
            "-fsSL",
            "--max-time",
            &secs,
            "--max-filesize",
            &max,
            "-A",
            "suzuri/1.0",
            "-o",
        ])
        .arg(&tmp)
        .arg(url.trim())
        .status()
        .map_err(|e| format!("download failed: {e}"))?;
    if !status.success() {
        let _ = fs::remove_file(&tmp);
        return Err("download failed".into());
    }
    let meta = fs::metadata(&tmp).map_err(|_| "download was empty".to_string())?;
    if meta.len() == 0 {
        let _ = fs::remove_file(&tmp);
        return Err("download was empty".into());
    }
    if meta.len() > MAX_BYTES {
        let _ = fs::remove_file(&tmp);
        return Err("image is too large (24 MB max)".into());
    }
    fs::rename(&tmp, &dest).map_err(|e| {
        let _ = fs::remove_file(&tmp);
        format!("couldn't cache wallpaper: {e}")
    })?;
    Ok(dest)
}

/// Decode a source already on disk (cache or original path).
pub fn load_from_path(path: &Path, source: &str) -> Result<WallpaperAsset, String> {
    let bytes = fs::read(path).map_err(|e| format!("couldn't read wallpaper: {e}"))?;
    if bytes.len() as u64 > MAX_BYTES {
        return Err("image is too large (24 MB max)".into());
    }
    decode_bytes(&bytes, source)
}

/// Resolve prefs source → asset (cache, then file, then URL fetch).
pub fn load_source(source: &str) -> Result<WallpaperAsset, String> {
    let source = source.trim();
    if source.is_empty() {
        return Err("no wallpaper".into());
    }
    if let Some(why) = reject_reason(source) {
        return Err(why.into());
    }
    let cache = cache_path(source);
    if cache.is_file() {
        if let Ok(asset) = load_from_path(&cache, source) {
            return Ok(asset);
        }
    }
    if is_http_url(source) {
        let cached = cache_url(source)?;
        return load_from_path(&cached, source);
    }
    let path = PathBuf::from(source);
    if !path.is_file() {
        return Err("wallpaper file is missing".into());
    }
    let cached = cache_local_file(&path).unwrap_or(path);
    load_from_path(&cached, source)
}

pub fn decode_bytes(bytes: &[u8], source: &str) -> Result<WallpaperAsset, String> {
    if bytes.is_empty() {
        return Err("empty image".into());
    }
    if looks_like_video(source) {
        return Err(reject_reason(source).unwrap_or("unsupported video").into());
    }
    if is_gif(bytes) {
        return decode_gif(bytes, source);
    }
    if is_webp(bytes) {
        return decode_webp(bytes, source);
    }
    decode_still(bytes, source)
}

fn is_gif(bytes: &[u8]) -> bool {
    bytes.starts_with(b"GIF87a") || bytes.starts_with(b"GIF89a")
}

fn is_webp(bytes: &[u8]) -> bool {
    bytes.len() >= 12 && bytes.starts_with(b"RIFF") && &bytes[8..12] == b"WEBP"
}

fn decode_still(bytes: &[u8], source: &str) -> Result<WallpaperAsset, String> {
    let img = image::load_from_memory(bytes).map_err(|e| format!("couldn't decode image: {e}"))?;
    let frame = rasterize(img)?;
    Ok(WallpaperAsset {
        source: source.to_string(),
        animated: false,
        frames: Arc::from([frame]),
    })
}

fn decode_gif(bytes: &[u8], source: &str) -> Result<WallpaperAsset, String> {
    use image::codecs::gif::GifDecoder;
    use image::AnimationDecoder;
    let decoder =
        GifDecoder::new(Cursor::new(bytes)).map_err(|e| format!("couldn't decode GIF: {e}"))?;
    let raw = decoder
        .into_frames()
        .collect_frames()
        .map_err(|e| format!("couldn't decode GIF: {e}"))?;
    pack_frames(raw, source)
}

fn decode_webp(bytes: &[u8], source: &str) -> Result<WallpaperAsset, String> {
    use image::codecs::webp::WebPDecoder;
    use image::AnimationDecoder;
    let decoder =
        WebPDecoder::new(Cursor::new(bytes)).map_err(|e| format!("couldn't decode WebP: {e}"))?;
    if !decoder.has_animation() {
        drop(decoder);
        return decode_still(bytes, source);
    }
    let raw = decoder
        .into_frames()
        .collect_frames()
        .map_err(|e| format!("couldn't decode WebP: {e}"))?;
    pack_frames(raw, source)
}

fn pack_frames(raw: Vec<image::Frame>, source: &str) -> Result<WallpaperAsset, String> {
    if raw.is_empty() {
        return Err("animation had no frames".into());
    }
    let step = ((raw.len() + MAX_FRAMES - 1) / MAX_FRAMES).max(1);
    let mut frames = Vec::new();
    for (i, f) in raw.into_iter().enumerate() {
        if i % step != 0 {
            continue;
        }
        let delay = delay_secs(f.delay());
        let frame = rasterize(image::DynamicImage::ImageRgba8(f.into_buffer()))?;
        frames.push(WallpaperFrame {
            rgba: frame.rgba,
            w: frame.w,
            h: frame.h,
            delay,
        });
        if frames.len() >= MAX_FRAMES {
            break;
        }
    }
    if frames.is_empty() {
        return Err("animation had no frames".into());
    }
    let animated = frames.len() > 1;
    Ok(WallpaperAsset {
        source: source.to_string(),
        animated,
        frames: frames.into(),
    })
}

fn delay_secs(delay: image::Delay) -> f32 {
    let (num, den) = delay.numer_denom_ms();
    let ms = if den == 0 {
        100.0
    } else {
        (num as f32) / (den as f32)
    };
    (ms / 1000.0).clamp(0.02, 5.0)
}

fn rasterize(img: image::DynamicImage) -> Result<WallpaperFrame, String> {
    let img = fit_max_edge(img, MAX_EDGE);
    let rgba = img.to_rgba8();
    let w = rgba.width();
    let h = rgba.height();
    if w == 0 || h == 0 {
        return Err("empty image".into());
    }
    Ok(WallpaperFrame {
        rgba: Arc::from(rgba.into_raw()),
        w,
        h,
        delay: 0.0,
    })
}

fn fit_max_edge(img: image::DynamicImage, max_edge: u32) -> image::DynamicImage {
    let w = img.width().max(1);
    let h = img.height().max(1);
    let edge = w.max(h);
    if edge <= max_edge {
        return img;
    }
    let scale = max_edge as f32 / edge as f32;
    let nw = ((w as f32) * scale).round().max(1.0) as u32;
    let nh = ((h as f32) * scale).round().max(1.0) as u32;
    img.resize(nw, nh, image::imageops::FilterType::Triangle)
}

/// Native file picker. `None` = cancelled.
pub fn pick_image_file() -> Option<PathBuf> {
    rfd::FileDialog::new()
        .set_title("Choose background")
        .add_filter(
            "Images",
            &["png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff"],
        )
        .add_filter("All files", &["*"])
        .pick_file()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn encode_png(w: u32, h: u32, rgba: &[u8]) -> Vec<u8> {
        let mut out = Vec::new();
        {
            let mut encoder = png::Encoder::new(&mut out, w, h);
            encoder.set_color(png::ColorType::Rgba);
            encoder.set_depth(png::BitDepth::Eight);
            let mut writer = encoder.write_header().unwrap();
            writer.write_image_data(rgba).unwrap();
        }
        out
    }

    #[test]
    fn decode_png_still() {
        let png = encode_png(2, 2, &[255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255, 255, 255, 0, 255]);
        let asset = decode_bytes(&png, "/tmp/x.png").unwrap();
        assert!(!asset.animated);
        assert_eq!(asset.frames.len(), 1);
        assert_eq!(asset.frames[0].w, 2);
        assert_eq!(asset.frames[0].h, 2);
        assert_eq!(&asset.frames[0].rgba[0..4], &[255, 0, 0, 255]);
    }

    #[test]
    fn rejects_youtube_and_video() {
        assert!(reject_reason("https://youtu.be/dQw4w9wgGcQ").is_some());
        assert!(reject_reason("https://www.youtube.com/watch?v=x").is_some());
        assert!(reject_reason("/movies/bg.mp4").is_some());
        assert!(reject_reason("https://example.com/bg.png").is_none());
        assert!(reject_reason("/tmp/wall.jpg").is_none());
    }

    #[test]
    fn display_label_filename_and_host() {
        assert_eq!(display_label(""), "none");
        assert_eq!(display_label("/Users/me/Pictures/forest.png"), "forest.png");
        let host = display_label("https://images.example.com/a/b.jpg");
        assert!(host.contains("images.example.com"), "{host}");
    }

    #[test]
    fn http_url_detect() {
        assert!(is_http_url("https://x.test/a.png"));
        assert!(is_http_url("  http://x.test/a.png"));
        assert!(!is_http_url("/tmp/a.png"));
        assert!(!is_http_url("file:///tmp/a.png"));
    }

    #[test]
    fn downscales_huge_edge() {
        let w = 64u32;
        let h = 32u32;
        let mut rgba = vec![0u8; (w * h * 4) as usize];
        for px in rgba.chunks_exact_mut(4) {
            px.copy_from_slice(&[10, 20, 30, 255]);
        }
        let png = encode_png(w, h, &rgba);
        let img = image::load_from_memory(&png).unwrap();
        let fit = fit_max_edge(img, 16);
        assert!(fit.width() <= 16 && fit.height() <= 16);
        assert_eq!(fit.width(), 16);
        assert_eq!(fit.height(), 8);
    }

    #[test]
    fn ext_lower_strips_query() {
        assert_eq!(ext_lower("https://x.test/a.PNG?w=1"), Some("png".into()));
        assert_eq!(ext_lower("clip.webm"), Some("webm".into()));
    }
}
