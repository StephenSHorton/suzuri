//! Pasting an image (Go suzuri's clipboard_image_*.go): the webview's paste
//! event hands the clipboard image over as raw bytes, this writes it to
//! <temp>/suzuri-paste/clip-<nanos>.<ext>, and the UI pastes that path into
//! the pane, so a full-screen agent (Claude Code, Grok) can attach it.
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use tauri::ipc::{InvokeBody, Request};

/// The file extension for an image's magic bytes, or None for anything that
/// isn't a PNG, JPEG, GIF, WebP or TIFF.
pub fn image_ext(b: &[u8]) -> Option<&'static str> {
    if b.starts_with(b"\x89PNG\r\n\x1a\n") {
        Some("png")
    } else if b.starts_with(b"\xff\xd8\xff") {
        Some("jpg")
    } else if b.starts_with(b"GIF87a") || b.starts_with(b"GIF89a") {
        Some("gif")
    } else if b.len() >= 12 && &b[..4] == b"RIFF" && &b[8..12] == b"WEBP" {
        Some("webp")
    } else if b.starts_with(b"II*\0") || b.starts_with(b"MM\0*") {
        Some("tiff")
    } else {
        None
    }
}

/// Writes the image under dir/suzuri-paste and returns its path.
pub fn save_image(dir: &Path, bytes: &[u8]) -> Result<PathBuf, String> {
    let ext = image_ext(bytes).ok_or("not an image")?;
    let dir = dir.join("suzuri-paste");
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700));
    }
    let nanos = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_nanos()).unwrap_or(0);
    let path = dir.join(format!("clip-{nanos}.{ext}"));
    std::fs::write(&path, bytes).map_err(|e| e.to_string())?;
    Ok(path)
}

/// The UI invokes this with the image's bytes as the raw body.
#[tauri::command]
pub fn paste_image_save(request: Request<'_>) -> Result<String, String> {
    let InvokeBody::Raw(bytes) = request.body() else {
        return Err("expected raw image bytes".into());
    };
    save_image(&std::env::temp_dir(), bytes).map(|p| p.to_string_lossy().into_owned())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sniffs_images_only() {
        assert_eq!(image_ext(b"\x89PNG\r\n\x1a\nrest"), Some("png"));
        assert_eq!(image_ext(b"\xff\xd8\xff\xe0"), Some("jpg"));
        assert_eq!(image_ext(b"RIFF\0\0\0\0WEBPVP8 "), Some("webp"));
        assert_eq!(image_ext(b"hello"), None);
    }

    #[test]
    fn saves_under_suzuri_paste() {
        let dir = std::env::temp_dir().join(format!("rsz-paste-test-{}", std::process::id()));
        let p = save_image(&dir, b"\x89PNG\r\n\x1a\nxx").unwrap();
        assert!(p.starts_with(dir.join("suzuri-paste")));
        assert_eq!(p.extension().unwrap(), "png");
        assert_eq!(std::fs::read(&p).unwrap(), b"\x89PNG\r\n\x1a\nxx");
        assert!(save_image(&dir, b"text").is_err());
        let _ = std::fs::remove_dir_all(&dir);
    }
}
