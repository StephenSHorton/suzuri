//! Tiny mock shell for the vertical slice until a real PTY is wired.
//! Port of surface `src/lib/mockShell.ts` — plain text (+ optional ANSI helpers).

/// ANSI snippets (optional; consumers may strip or ignore).
pub const PROMPT_GLYPH: &str = "❯ ";
pub const DIM: &str = "\x1b[90m";
pub const JADE: &str = "\x1b[92m";
pub const ERR: &str = "\x1b[91m";
pub const GREEN: &str = "\x1b[32m";
pub const RESET: &str = "\x1b[0m";

/// Result of running a mock command.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum ShellOutput {
    /// Wipe the terminal and re-draw the prompt.
    Clear,
    /// Lines of shell output (no trailing newlines required).
    Lines(Vec<String>),
}

/// Banner shown on boot (plain text; style in the session/grid layer).
pub fn banner_lines() -> Vec<String> {
    vec![
        "suzuri surface  ·  Kussetsu chrome + cell-grid terminal".into(),
        "rule: anything that isn't shell output never snaps to a character cell".into(),
        String::new(),
        "mock PTY — try: help, clear, uname, whoami, theme, rain".into(),
        String::new(),
    ]
}

/// Multi-line prompt: host line, then the `❯ ` glyph line (no trailing newline on last).
pub fn prompt_lines() -> Vec<String> {
    vec![
        "stephen@inkstone ~/projects/suzuri".into(),
        PROMPT_GLYPH.into(),
    ]
}

/// Single-string prompt with a newline between host and glyph (plain).
pub fn prompt() -> String {
    format!("stephen@inkstone ~/projects/suzuri\n{PROMPT_GLYPH}")
}

/// ANSI-decorated prompt matching the TS surface spike (optional).
pub fn prompt_ansi() -> String {
    format!("{DIM}stephen@inkstone ~/projects/suzuri{RESET}\r\n{GREEN}❯{RESET} ")
}

/// Run a mock command. Empty / whitespace-only lines yield no output.
pub fn run_command(line: &str) -> ShellOutput {
    let trimmed = line.trim();
    if trimmed.is_empty() {
        return ShellOutput::Lines(vec![]);
    }

    if trimmed == "clear" {
        return ShellOutput::Clear;
    }

    if trimmed == "help" {
        return ShellOutput::Lines(vec![
            "mock commands: help, clear, uname, whoami, theme, rain".into(),
            "(real ConPTY / POSIX PTY comes later)".into(),
        ]);
    }

    if trimmed.starts_with("uname") {
        return ShellOutput::Lines(vec!["Darwin arm64".into()]);
    }

    if trimmed == "whoami" {
        return ShellOutput::Lines(vec!["surface-slice".into()]);
    }

    if trimmed == "theme" {
        return ShellOutput::Lines(vec![
            "inkstone · Kussetsu GPU chrome · cell pane only for shell".into(),
        ]);
    }

    if trimmed == "rain" {
        return ShellOutput::Lines(vec![
            "glyph rain is real half-width katakana + digits (Canvas 2D), Canvas UI–style.".into(),
            "chrome is Kussetsu WebGPU glass; cells stay in the xterm hole.".into(),
        ]);
    }

    let cmd = trimmed.split_whitespace().next().unwrap_or(trimmed);
    ShellOutput::Lines(vec![
        format!("mock: command not found: {cmd}"),
        "(PTY not connected — UI architecture slice)".into(),
    ])
}

/// Strip CSI / simple ESC sequences for plain-text cell writing.
pub fn strip_ansi(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut chars = s.chars().peekable();
    while let Some(c) = chars.next() {
        if c == '\x1b' {
            if chars.peek() == Some(&'[') {
                chars.next();
                for d in chars.by_ref() {
                    if d.is_ascii_alphabetic() {
                        break;
                    }
                }
            }
            continue;
        }
        out.push(c);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn clear_command() {
        assert_eq!(run_command("clear"), ShellOutput::Clear);
        assert_eq!(run_command("  clear  "), ShellOutput::Clear);
    }

    #[test]
    fn help_and_unknown() {
        match run_command("help") {
            ShellOutput::Lines(lines) => assert!(lines[0].contains("mock commands")),
            _ => panic!("expected lines"),
        }
        match run_command("nope") {
            ShellOutput::Lines(lines) => {
                assert!(lines[0].contains("command not found: nope"));
            }
            _ => panic!("expected lines"),
        }
    }

    #[test]
    fn strip_ansi_basic() {
        assert_eq!(strip_ansi("\x1b[32m❯\x1b[0m hi"), "❯ hi");
    }
}
