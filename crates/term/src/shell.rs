//! Shell integration for the input bar (#285), ported from Go suzuri
//! (internal/host/session_unix.go, session_windows.go, ui/term_protocols.go).
//!
//! The shell is started with a small bootstrap that:
//! - blanks its prompt (the input bar owns the command line);
//! - reports the working directory at every prompt: `OSC 7878;cwd=<path> BEL`;
//! - marks a command starting (`OSC 7879;start BEL`, from preexec) and
//!   finishing (`OSC 7879;done;<exit>;<base64 command> BEL`, from precmd).
//!
//! [`OscFilter`] takes those sequences out of the PTY stream before the VT
//! engine sees it, and turns them into [`ShellEvent`]s.

use std::path::{Path, PathBuf};

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ShellEvent {
    /// The prompt is back, in this directory.
    Cwd(String),
    /// A command started (preexec): a program owns the keyboard until the
    /// next prompt.
    Start,
    /// A command finished, with its exit status and command line when known.
    Done { exit: Option<i32>, command: String },
}

/// Removes suzuri's private OSCs (7878, 7879) from a byte stream. Other
/// escape sequences pass through untouched. A sequence split across reads is
/// held back until it is complete.
#[derive(Default)]
pub struct OscFilter {
    /// Bytes of a possible suzuri OSC not yet resolved: starts with ESC.
    pending: Vec<u8>,
}

/// Longest OSC payload held back before it is given up on and passed through.
const MAX_OSC: usize = 64 * 1024;

/// Only these forms are ours. `7879;image=` (inline images, #288) and any
/// other 7879 kind pass through to the next filter.
const PREFIXES: [&[u8]; 3] = [b"\x1b]7878;cwd=", b"\x1b]7879;start", b"\x1b]7879;done"];

impl OscFilter {
    /// Filter `input`, appending the bytes for the VT engine to `out` and
    /// any shell events to `events`.
    pub fn feed(&mut self, input: &[u8], out: &mut Vec<u8>, events: &mut Vec<ShellEvent>) {
        if self.pending.is_empty() {
            self.feed_from(input, 0, out, events);
        } else {
            let mut joined = std::mem::take(&mut self.pending);
            joined.extend_from_slice(input);
            self.feed_from(&joined, 0, out, events);
        }
    }

    fn feed_from(&mut self, buf: &[u8], mut i: usize, out: &mut Vec<u8>, events: &mut Vec<ShellEvent>) {
        while i < buf.len() {
            let Some(k) = memchr_esc(&buf[i..]) else {
                out.extend_from_slice(&buf[i..]);
                return;
            };
            out.extend_from_slice(&buf[i..i + k]);
            i += k;
            let rest = &buf[i..];
            // Could this be one of ours? Compare as much of the prefix as we have.
            let ours = PREFIXES.iter().any(|p| {
                let n = p.len().min(rest.len());
                rest[..n] == p[..n]
            });
            if !ours {
                out.push(0x1b);
                i += 1;
                continue;
            }
            // Not yet long enough to know: hold it.
            if PREFIXES.iter().all(|p| rest.len() < p.len()) {
                self.pending.extend_from_slice(rest);
                return;
            }
            // Find the terminator: BEL or ESC \.
            let body = &rest[2..];
            let end = body.iter().enumerate().find_map(|(j, &b)| match b {
                0x07 => Some((j, 1)),
                0x1b if body.get(j + 1) == Some(&b'\\') => Some((j, 2)),
                _ => None,
            });
            match end {
                Some((j, term)) => {
                    if let Some(ev) = parse(&body[..j]) {
                        events.push(ev);
                    }
                    i += 2 + j + term;
                }
                None if rest.len() > MAX_OSC => {
                    // Never terminated: not ours after all.
                    out.extend_from_slice(rest);
                    return;
                }
                None => {
                    self.pending.extend_from_slice(rest);
                    return;
                }
            }
        }
    }
}

fn memchr_esc(b: &[u8]) -> Option<usize> {
    b.iter().position(|&c| c == 0x1b)
}

fn parse(payload: &[u8]) -> Option<ShellEvent> {
    let s = String::from_utf8_lossy(payload);
    if let Some(path) = s.strip_prefix("7878;cwd=") {
        return Some(ShellEvent::Cwd(path.to_string()));
    }
    let rest = s.strip_prefix("7879;")?;
    let mut parts = rest.splitn(3, ';');
    match parts.next()? {
        "start" => Some(ShellEvent::Start),
        "done" => {
            let exit = parts.next().and_then(|e| e.trim().parse().ok());
            let command = parts.next().map(decode_b64).unwrap_or_default();
            Some(ShellEvent::Done { exit, command })
        }
        _ => None,
    }
}

fn decode_b64(s: &str) -> String {
    const T: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = Vec::new();
    let (mut acc, mut bits) = (0u32, 0u32);
    for c in s.bytes() {
        let Some(v) = T.iter().position(|&t| t == c) else { continue };
        acc = (acc << 6) | v as u32;
        bits += 6;
        if bits >= 8 {
            bits -= 8;
            out.push((acc >> bits) as u8);
        }
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// How a shell is started with the bootstrap: extra args, env, and a temp
/// dir to remove when the pane closes.
pub struct Bootstrap {
    pub argv: Vec<String>,
    pub env: Vec<(String, String)>,
    pub temp_dir: Option<PathBuf>,
}

/// Wrap `argv` (the user's shell) with the bootstrap, when it is a shell we
/// know (zsh, bash, PowerShell) started without its own `-c` or rc file.
/// Anything else (a custom command, fish, cmd.exe) runs as is, and the bar
/// stays off for it: without the prompt marks there is no telling when a
/// program owns the keyboard.
pub fn bootstrap(argv: &[String]) -> Option<Bootstrap> {
    let exe = argv.first()?;
    let base = Path::new(exe).file_name()?.to_string_lossy().to_lowercase();
    let base = base.strip_suffix(".exe").unwrap_or(&base).to_string();
    let rest: Vec<String> = argv[1..].iter().map(|a| a.to_lowercase()).collect();
    let custom = rest.iter().any(|a| {
        a == "-c" || a == "-command" || a.contains("rcfile") || a.contains("norc") || a.contains("noprofile") || a == "-file"
    });
    if custom {
        return None;
    }
    match base.as_str() {
        "zsh" => {
            let dir = temp_dir("rsz-zdot")?;
            std::fs::write(dir.join(".zshrc"), ZSHRC).ok()?;
            std::fs::write(dir.join(".zshenv"), "# suzuri\n").ok()?;
            let user = std::env::var("ZDOTDIR").ok().filter(|s| !s.is_empty()).or_else(home).unwrap_or_default();
            Some(Bootstrap {
                argv: argv.to_vec(),
                env: vec![
                    ("ZDOTDIR".into(), dir.to_string_lossy().into_owned()),
                    ("RSZ_USER_ZDOTDIR".into(), user),
                ],
                temp_dir: Some(dir),
            })
        }
        "bash" => {
            let dir = temp_dir("rsz-bash")?;
            let rc = dir.join("bashrc");
            std::fs::write(&rc, BASHRC).ok()?;
            // --rcfile is ignored by a login shell, so run interactive and let
            // the rc file load the user's login files itself.
            let mut a = vec![exe.clone(), "--rcfile".into(), rc.to_string_lossy().into_owned(), "-i".into()];
            a.extend(argv[1..].iter().filter(|x| *x != "-l" && *x != "--login").cloned());
            Some(Bootstrap { argv: a, env: vec![], temp_dir: Some(dir) })
        }
        "pwsh" | "powershell" => {
            let mut a = argv.to_vec();
            a.extend(["-NoExit".into(), "-Command".into(), PWSH_PROMPT.into()]);
            Some(Bootstrap { argv: a, env: vec![], temp_dir: None })
        }
        _ => None,
    }
}

fn home() -> Option<String> {
    std::env::var("HOME").ok().or_else(|| std::env::var("USERPROFILE").ok())
}

fn temp_dir(prefix: &str) -> Option<PathBuf> {
    use std::sync::atomic::{AtomicU32, Ordering};
    static N: AtomicU32 = AtomicU32::new(0);
    let dir = std::env::temp_dir().join(format!("{prefix}-{}-{}", std::process::id(), N.fetch_add(1, Ordering::Relaxed)));
    std::fs::create_dir_all(&dir).ok()?;
    Some(dir)
}

/// zsh: load the user's own files from their ZDOTDIR, then blank the prompt
/// from a precmd hook that runs last, so themes cannot repaint one.
const ZSHRC: &str = r#"# suzuri bootstrap: the input bar owns the command line.
_rsz_home=${RSZ_USER_ZDOTDIR:-$HOME}
ZDOTDIR=$_rsz_home
[[ -f "$_rsz_home/.zshenv" ]] && source "$_rsz_home/.zshenv"
[[ -o login && -f "$_rsz_home/.zprofile" ]] && source "$_rsz_home/.zprofile"
[[ -f "$_rsz_home/.zshrc" ]] && source "$_rsz_home/.zshrc"
[[ -o login && -f "$_rsz_home/.zlogin" ]] && source "$_rsz_home/.zlogin"
unset RSZ_USER_ZDOTDIR
_rsz_quiet_prompt() {
  PROMPT=' '
  RPROMPT=''
  PS1=' '
  unset RPS1
  PROMPT_EOL_MARK=''
  printf '\033]7878;cwd=%s\007' "$PWD"
}
_rsz_preexec() {
  [[ ${_rsz_depth:-0} != 0 ]] && return 0
  _rsz_cmd=$1
  printf '\033]7879;start\007'
}
_rsz_precmd() {
  local ec=$?
  [[ ${_rsz_depth:-0} != 0 ]] && return 0
  _rsz_depth=1
  if [[ -n ${_rsz_cmd:-} ]]; then
    local b64
    b64=$(printf '%s' "$_rsz_cmd" | command base64 | tr -d '\n')
    printf '\033]7879;done;%s;%s\007' "$ec" "$b64"
    _rsz_cmd=
  fi
  _rsz_quiet_prompt
  _rsz_depth=0
}
precmd_functions+=(_rsz_precmd)
preexec_functions+=(_rsz_preexec)
"#;

const BASHRC: &str = r#"# suzuri bootstrap: the input bar owns the command line.
# The shell was asked for as a login shell; --rcfile needs -i instead, so
# load the login files here. ~/.bash_profile conventionally sources ~/.bashrc.
if [[ -f "$HOME/.bash_profile" ]]; then source "$HOME/.bash_profile"
elif [[ -f "$HOME/.bash_login" ]]; then source "$HOME/.bash_login"
else
  [[ -f "$HOME/.profile" ]] && source "$HOME/.profile"
  [[ -f "$HOME/.bashrc" ]] && source "$HOME/.bashrc"
fi
_rsz_cmd=
_rsz_ec=0
_rsz_prompt() {
  _rsz_in_prompt=1
  PS1=' '
  if [[ -n ${_rsz_cmd:-} ]]; then
    local b64
    b64=$(printf '%s' "$_rsz_cmd" | command base64 | tr -d '\n')
    printf '\033]7879;done;%s;%s\007' "$_rsz_ec" "$b64"
    _rsz_cmd=
  fi
  printf '\033]7878;cwd=%s\007' "$PWD"
  _rsz_in_prompt=
}
_rsz_debug() {
  [[ -n ${_rsz_in_prompt:-} ]] && return
  case $BASH_COMMAND in
    _rsz_*) return ;;
  esac
  [[ -n ${_rsz_cmd:-} ]] && return
  _rsz_cmd=$BASH_COMMAND
  printf '\033]7879;start\007'
}
PROMPT_COMMAND='_rsz_ec=$?; _rsz_prompt'
trap _rsz_debug DEBUG
"#;

/// PowerShell: a prompt function that reports the last command and the cwd
/// and prints a blank prompt. PowerShell has no preexec, so a command start
/// is known from the bar's own Enter and from child processes.
const PWSH_PROMPT: &str = "function global:prompt { $ok=$?; $native=$global:LASTEXITCODE; try { $h=Get-History -Count 1 -ErrorAction SilentlyContinue; if ($h -and $h.Id -ne $global:RszHist) { $global:RszHist=$h.Id; $ec=0; if (-not $ok) { if ($native) { $ec=[int]$native } else { $ec=1 } }; $b64=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([string]$h.CommandLine)); $e=[char]27; $b=[char]7; [Console]::Out.Write(('{0}]7879;done;{1};{2}{3}' -f $e,$ec,$b64,$b)) }; $p=(Get-Location).ProviderPath; if(-not $p){$p=(Get-Location).Path}; $e=[char]27; $b=[char]7; [Console]::Out.Write(($e+']7878;cwd='+$p+$b)) } catch {}; ' ' }; Clear-Host";

#[cfg(test)]
mod tests {
    use super::*;

    fn run(chunks: &[&[u8]]) -> (Vec<u8>, Vec<ShellEvent>) {
        let mut f = OscFilter::default();
        let (mut out, mut ev) = (Vec::new(), Vec::new());
        for c in chunks {
            f.feed(c, &mut out, &mut ev);
        }
        (out, ev)
    }

    #[test]
    fn strips_ours_and_keeps_the_rest() {
        let (out, ev) = run(&[b"a\x1b]7878;cwd=/tmp/x y\x07b\x1b[31mc\x1b]0;title\x07d\x1b]7879;start\x1b\\e"]);
        assert_eq!(out, b"ab\x1b[31mc\x1b]0;title\x07de");
        assert_eq!(ev, vec![ShellEvent::Cwd("/tmp/x y".into()), ShellEvent::Start]);
    }

    #[test]
    fn other_7879_kinds_pass_through() {
        let (out, ev) = run(&[b"a\x1b]7879;image=/tmp/a.png\x07b", b"\x1b]7879;sta", b"rt\x07"]);
        assert_eq!(out, b"a\x1b]7879;image=/tmp/a.png\x07b");
        assert_eq!(ev, vec![ShellEvent::Start]);
    }

    #[test]
    fn done_decodes_exit_and_command() {
        // "ls -la" in base64 is bHMgLWxh
        let (out, ev) = run(&[b"\x1b]7879;done;2;bHMgLWxh\x07"]);
        assert!(out.is_empty());
        assert_eq!(ev, vec![ShellEvent::Done { exit: Some(2), command: "ls -la".into() }]);
    }

    #[test]
    fn split_across_reads_anywhere() {
        let whole: &[u8] = b"x\x1b]7878;cwd=/a\x07y\x1b]7879;start\x07z";
        for cut in 0..whole.len() {
            let (out, ev) = run(&[&whole[..cut], &whole[cut..]]);
            assert_eq!(out, b"xyz", "cut at {cut}");
            assert_eq!(ev, vec![ShellEvent::Cwd("/a".into()), ShellEvent::Start], "cut at {cut}");
        }
        // A lone ESC at the end of a read that turns out to be CSI.
        let (out, _) = run(&[b"a\x1b", b"[2Jb"]);
        assert_eq!(out, b"a\x1b[2Jb");
    }

    #[test]
    fn bootstrap_only_for_known_shells_without_custom_args() {
        assert!(bootstrap(&["/bin/zsh".into(), "-l".into()]).is_some());
        assert!(bootstrap(&["/bin/bash".into(), "-l".into()]).unwrap().argv.contains(&"--rcfile".to_string()));
        assert!(bootstrap(&["pwsh.exe".into(), "-NoLogo".into()]).is_some());
        assert!(bootstrap(&["/bin/zsh".into(), "-c".into(), "ls".into()]).is_none());
        assert!(bootstrap(&["/usr/bin/fish".into()]).is_none());
        assert!(bootstrap(&["claude".into()]).is_none());
    }
}
