//! A submitted bar line as Go suzuri shows it (#296): a command block in
//! the scrollback (internal/ui/scrollback.go pushBlock) and the bare-cd
//! alias (cwd.go expandBarSubmit).
//!
//! The block goes into the grid just above the cursor's line (the engine
//! scrolls what is there up into the history), so it scrolls, selects and
//! copies with the rest while the cursor stays on the row the shell (and
//! ConPTY, which repaints by absolute row) expects: a blank line, a rule
//! across the pane in a soft primary, then `<cwd> ❯ <command>` in the
//! primary (continuation lines indented under the command).

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

/// The block's lines with their colours: an optional spacer, the rule, then
/// the command (continuation lines indented under it).
pub fn block_lines(display: &str, cwd: &str, home: &str, cols: u16, spacer: bool, primary: Rgb) -> Vec<(String, Rgb)> {
    let cols = cols.max(12) as usize;
    let rule = (primary.0 / 2 + 40, primary.1 / 2 + 40, primary.2 / 2 + 40);
    let mut out = Vec::new();
    if spacer {
        out.push((String::new(), primary));
    }
    out.push(("─".repeat(cols), rule));
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
        out.push((truncate_end(&line, cols), primary));
    }
    out
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
        let l = block_lines("echo hi", "/h/u/src", "/h/u", 20, true, (200, 100, 50));
        assert_eq!(l, vec![
            (String::new(), (200, 100, 50)),
            ("─".repeat(20), (140, 90, 65)),
            ("~/src ❯ echo hi".to_string(), (200, 100, 50)),
        ]);
        // A blank line already above: no spacer.
        assert_eq!(block_lines("x", "", "", 20, false, (0, 0, 0)).len(), 2);
    }

    #[test]
    fn multi_line_and_long_path() {
        let l = block_lines("a\nb", "/very/long/path/to/somewhere/deep", "", 24, false, (1, 2, 3));
        // Path budget is at most half the width; the end of the path stays.
        assert_eq!(l[1].0, "…ewhere/deep ❯ a");
        assert_eq!(l[2].0, "  b");
    }
}
