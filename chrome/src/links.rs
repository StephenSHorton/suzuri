//! Terminal URL detection — hover spans + open-in-browser.
//!
//! Port of product `internal/ui/links.go`: same regex, trail-punct trim,
//! unbalanced `)` trim, `www.` → `https://`, and platform browser launch.

use std::process::Command;
use std::sync::LazyLock;

use regex::Regex;

/// URL span on one line: `x0` inclusive, `x1` exclusive (cell / char columns).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct LinkSpan {
    pub x0: usize,
    pub x1: usize,
    pub url: String,
}

/// Product `urlPattern`: http(s) and www. URLs in terminal text.
static URL_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?i)\b(?:https?://|www\.)[^\s<>"'{}|\\^`\[\]()]+"#)
        .expect("url pattern")
});

/// Find URL spans in a single terminal line (one cell ≈ one char).
///
/// Matches product `findLinksInGrid` row scan: trail punctuation and unbalanced
/// closing parens are trimmed from the match before column bounds and normalize.
pub fn find_links_in_line(line: &str) -> Vec<LinkSpan> {
    let mut out = Vec::new();
    for m in URL_PATTERN.find_iter(line) {
        let b0 = m.start();
        let b1 = m.end();
        if b0 >= b1 || b1 > line.len() {
            continue;
        }
        let raw = &line[b0..b1];
        let mut rr: Vec<char> = raw.chars().collect();
        while rr.last().copied().is_some_and(is_url_trail_punct) {
            rr.pop();
        }
        // Unbalanced closing paren: (see https://x.com) → drop ')'
        while rr.last() == Some(&')') {
            let s: String = rr.iter().collect();
            let open = s.matches('(').count();
            let close = s.matches(')').count();
            if open >= close {
                break;
            }
            rr.pop();
        }
        if rr.len() < 4 {
            continue;
        }
        let trimmed: String = rr.iter().collect();
        let url = normalize_url(&trimmed);
        if url.is_empty() {
            continue;
        }
        let x0 = line[..b0].chars().count();
        let x1 = x0 + rr.len();
        if x1 <= x0 {
            continue;
        }
        out.push(LinkSpan { x0, x1, url });
    }
    out
}

/// Return the link under column `col` (0-based cell), if any.
pub fn link_at(spans: &[LinkSpan], col: usize) -> Option<&LinkSpan> {
    spans.iter().find(|s| col >= s.x0 && col < s.x1)
}

/// Find the URL under `col` on `line`, if any.
pub fn link_url_at_col(line: &str, col: usize) -> Option<String> {
    let spans = find_links_in_line(line);
    link_at(&spans, col).map(|s| s.url.clone())
}

fn is_url_trail_punct(r: char) -> bool {
    matches!(
        r,
        '.' | ',' | ';' | ':' | '!' | '?' | ']' | '}' | '\'' | '"' | '»' | '›'
    ) || r.is_whitespace()
}

/// Normalize a matched URL string (`www.` → `https://`, require http(s) host).
pub fn normalize_url(raw: &str) -> String {
    let mut raw = raw.trim().to_string();
    if raw.is_empty() {
        return String::new();
    }
    let lower = raw.to_ascii_lowercase();
    if lower.starts_with("www.") {
        raw = format!("https://{raw}");
    }
    let lower = raw.to_ascii_lowercase();
    if !lower.starts_with("http://") && !lower.starts_with("https://") {
        return String::new();
    }
    let rest = if let Some(i) = raw.find("://") {
        &raw[i + 3..]
    } else {
        return String::new();
    };
    if rest.is_empty() || rest.starts_with('/') {
        return String::new();
    }
    raw
}

/// Free-text clean: trail punct + paren trim, then [`normalize_url`].
/// Used by tests and any non-grid open path.
pub fn clean_url(raw: &str) -> String {
    let mut rr: Vec<char> = raw.trim().chars().collect();
    while rr.last().copied().is_some_and(is_url_trail_punct) {
        rr.pop();
    }
    while rr.last() == Some(&')') {
        let s: String = rr.iter().collect();
        let open = s.matches('(').count();
        let close = s.matches(')').count();
        if open >= close {
            break;
        }
        rr.pop();
    }
    let s: String = rr.into_iter().collect();
    normalize_url(&s)
}

/// Launch the system default browser for `url` (non-blocking).
///
/// macOS: `open`, Linux: `xdg-open`, Windows: `cmd /c start "" url`.
/// Does **not** use the `open` crate binary path for launching chrome itself.
pub fn open_url_in_browser(url: &str) {
    let url = url.trim();
    if url.is_empty() {
        return;
    }
    let result = {
        #[cfg(target_os = "macos")]
        {
            Command::new("open").arg(url).spawn()
        }
        #[cfg(target_os = "windows")]
        {
            Command::new("cmd")
                .args(["/c", "start", "", url])
                .spawn()
        }
        #[cfg(not(any(target_os = "macos", target_os = "windows")))]
        {
            Command::new("xdg-open").arg(url).spawn()
        }
    };
    match result {
        Ok(mut child) => {
            // Detach: don't leave zombies; ignore wait errors.
            std::thread::spawn(move || {
                let _ = child.wait();
            });
        }
        Err(e) => {
            eprintln!("suzuri-chrome: open url failed ({url}): {e}");
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn clean_url_cases() {
        let cases = [
            ("https://example.com/foo", "https://example.com/foo"),
            ("https://example.com/foo.", "https://example.com/foo"),
            ("www.example.com", "https://www.example.com"),
            ("https://x.com)", "https://x.com"),
            ("not a url", ""),
            ("http://", ""),
        ];
        for (inp, want) in cases {
            assert_eq!(clean_url(inp), want, "clean_url({inp:?})");
        }
    }

    #[test]
    fn find_links_example_com() {
        // "see https://example.com/x now"
        let line = "see https://example.com/x now";
        let spans = find_links_in_line(line);
        assert_eq!(spans.len(), 1, "spans={spans:?}");
        let s = &spans[0];
        assert_eq!(s.url, "https://example.com/x");
        assert_eq!(s.x0, 4); // after "see "
        // "https://example.com/x" = 21 chars → x1 = 4+21 = 25
        assert_eq!(s.x1, 25);
        assert!(link_at(&spans, 10).is_some());
        assert!(link_at(&spans, 0).is_none());
    }

    #[test]
    fn find_links_www() {
        let line = "www.github.com/foo";
        let spans = find_links_in_line(line);
        assert_eq!(spans.len(), 1);
        assert_eq!(spans[0].url, "https://www.github.com/foo");
        assert_eq!(spans[0].x0, 0);
        assert_eq!(spans[0].x1, line.chars().count());
    }

    #[test]
    fn find_links_trailing_punct() {
        let line = "visit https://example.com/path.";
        let spans = find_links_in_line(line);
        assert_eq!(spans.len(), 1);
        assert_eq!(spans[0].url, "https://example.com/path");
        // trailing '.' trimmed from span
        assert_eq!(spans[0].x1, spans[0].x0 + "https://example.com/path".len());
    }

    #[test]
    fn find_links_paren_trim() {
        let line = "(see https://x.com)";
        let spans = find_links_in_line(line);
        assert_eq!(spans.len(), 1);
        assert_eq!(spans[0].url, "https://x.com");
        assert!(link_at(&spans, spans[0].x0).is_some());
        // closing ')' is outside the span
        assert!(link_at(&spans, line.chars().count() - 1).is_none());
    }

    #[test]
    fn normalize_rejects_empty_host() {
        assert_eq!(normalize_url("https://"), "");
        assert_eq!(normalize_url("http:///path"), "");
        assert_eq!(normalize_url("ftp://example.com"), "");
    }

    #[test]
    fn link_url_at_col_hits() {
        let line = "go https://a.co/x please";
        assert_eq!(
            link_url_at_col(line, 5).as_deref(),
            Some("https://a.co/x")
        );
        assert_eq!(link_url_at_col(line, 0), None);
    }
}
