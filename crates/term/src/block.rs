//! A submitted bar line as Go suzuri shows it (#296): a command block in
//! the scrollback (internal/ui/scrollback.go pushBlock) and the bare-cd
//! alias (cwd.go expandBarSubmit).
//!
//! The block is written into the grid like any output, so it scrolls,
//! selects and copies with the rest: a blank line, a rule across the pane in
//! a soft primary, then `<cwd> ❯ <command>` in the primary (continuation
//! lines indented under the command).

use crate::palette::Rgb;

pub const PROMPT: &str = "❯ ";

/// A bar line as (display, payload): what the block shows and what the
/// shell gets. Only a bare `cd` changes: home, in each shell's own words.
pub fn expand(line: &str, shell: &str) -> (String, String) {
    let t = line.trim().to_ascii_lowercase();
    if !matches!(t.as_str(), "cd" | "chdir" | "set-location" | "sl") {
        return (line.to_string(), line.to_string());
    }
    let base = shell_base(shell);
    if base == "cmd" || base == "cmd.exe" {
        ("cd ~".into(), "cd /d \"%USERPROFILE%\"".into())
    } else if base.contains("powershell") || base == "pwsh" || base == "pwsh.exe" {
        ("cd ~".into(), "Set-Location ~".into())
    } else {
        ("cd".into(), "cd".into())
    }
}

/// The shell's program name, lower case, without its directory.
pub fn shell_base(shell: &str) -> String {
    let name = shell.rsplit(['/', '\\']).next().unwrap_or(shell);
    name.to_ascii_lowercase()
}

/// `path` with the home directory shown as ~.
pub fn display_path(path: &str, home: &str) -> String {
    if home.is_empty() || path.is_empty() {
        return path.to_string();
    }
    match path.strip_prefix(home) {
        Some("") => "~".into(),
        Some(rest) if rest.starts_with(['/', '\\']) => format!("~{rest}"),
        _ => path.to_string(),
    }
}

fn truncate(s: &str, n: usize) -> String {
    let c: Vec<char> = s.chars().collect();
    if c.len() <= n {
        return s.to_string();
    }
    if n <= 1 {
        return c[..n].iter().collect();
    }
    // Keep the end of a long path: that's the part that tells it apart.
    let mut out = String::from("…");
    out.extend(&c[c.len() - (n - 1)..]);
    out
}

fn sgr(c: Rgb) -> String {
    format!("\x1b[0;38;2;{};{};{}m", c.0, c.1, c.2)
}

/// The bytes that draw a block for `display`, given the pane's width,
/// whether the cursor's line is blank and whether a blank line already sits
/// above it (then no spacer is added).
pub fn block_bytes(display: &str, cwd: &str, home: &str, cols: u16, at_line_start: bool, blank_above: bool, primary: Rgb) -> Vec<u8> {
    let cols = cols.max(12) as usize;
    let rule = (primary.0 / 2 + 40, primary.1 / 2 + 40, primary.2 / 2 + 40);
    let mut s = String::new();
    if at_line_start {
        // On a blank line (zsh's prompt leaves one space): it is the spacer,
        // or the rule's line when a blank one is already above.
        s.push('\r');
        if !blank_above {
            s.push_str("\r\n");
        }
    } else {
        s.push_str("\r\n\r\n");
    }
    s.push_str(&sgr(rule));
    s.push_str(&"─".repeat(cols));
    s.push_str("\x1b[0m\r\n");
    let path = display_path(cwd, home);
    let indent = " ".repeat(PROMPT.chars().count());
    for (i, part) in display.replace("\r\n", "\n").split('\n').enumerate() {
        let line = if i > 0 {
            format!("{indent}{part}")
        } else if path.is_empty() {
            format!("{PROMPT}{part}")
        } else {
            let used = PROMPT.chars().count() + part.chars().count();
            let budget = (cols.saturating_sub(used + 1)).clamp(6, cols / 2);
            format!("{} {PROMPT}{part}", truncate(&path, budget))
        };
        s.push_str(&sgr(primary));
        s.push_str(&truncate_end(&line, cols));
        s.push_str("\x1b[0m\r\n");
    }
    s.into_bytes()
}

fn truncate_end(s: &str, n: usize) -> String {
    s.chars().take(n).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bare_cd_per_shell() {
        assert_eq!(expand("cd", "/bin/zsh"), ("cd".into(), "cd".into()));
        assert_eq!(expand(" CD ", r"C:\Program Files\PowerShell\7\pwsh.exe"), ("cd ~".into(), "Set-Location ~".into()));
        assert_eq!(expand("cd", r"C:\Windows\System32\cmd.exe"), ("cd ~".into(), "cd /d \"%USERPROFILE%\"".into()));
        assert_eq!(expand("cd src", "pwsh"), ("cd src".into(), "cd src".into()));
        assert_eq!(expand("ls -la", "bash"), ("ls -la".into(), "ls -la".into()));
    }

    #[test]
    fn home_shows_as_tilde() {
        assert_eq!(display_path("/Users/a/projects", "/Users/a"), "~/projects");
        assert_eq!(display_path("/Users/a", "/Users/a"), "~");
        assert_eq!(display_path("/Users/ab", "/Users/a"), "/Users/ab");
        assert_eq!(display_path(r"C:\Users\a\x", r"C:\Users\a"), r"~\x");
    }

    #[test]
    fn block_draws_rule_and_command() {
        let b = String::from_utf8(block_bytes("echo hi", "/h/u/src", "/h/u", 20, true, false, (200, 100, 50))).unwrap();
        let plain: String = strip_sgr(&b);
        assert_eq!(plain, format!("\r\r\n{}\r\n~/src ❯ echo hi\r\n", "─".repeat(20)));
        assert!(b.contains("38;2;200;100;50m~/src"));
        assert!(b.contains("38;2;140;90;65m─"));
        // Not at a line start: a newline first.
        let b = block_bytes("x", "", "", 20, false, false, (0, 0, 0));
        assert!(strip_sgr(std::str::from_utf8(&b).unwrap()).starts_with("\r\n\r\n─"));
        // A blank line already above the cursor is the spacer.
        let b = block_bytes("x", "", "", 20, true, true, (0, 0, 0));
        assert!(strip_sgr(std::str::from_utf8(&b).unwrap()).starts_with("\r─"));
    }

    #[test]
    fn multi_line_and_long_path() {
        let b = String::from_utf8(block_bytes("a\nb", "/very/long/path/to/somewhere/deep", "", 24, true, false, (1, 2, 3))).unwrap();
        let lines: Vec<String> = strip_sgr(&b).split("\r\n").map(String::from).collect();
        // Path budget is at most half the width; the end of the path stays.
        assert_eq!(lines[2], "…ewhere/deep ❯ a");
        assert_eq!(lines[3], "  b");
    }

    fn strip_sgr(s: &str) -> String {
        let mut out = String::new();
        let mut it = s.chars().peekable();
        while let Some(c) = it.next() {
            if c == '\x1b' {
                for d in it.by_ref() {
                    if d == 'm' {
                        break;
                    }
                }
                continue;
            }
            out.push(c);
        }
        out
    }
}
