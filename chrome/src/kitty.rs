//! Kitty keyboard protocol — progressive enhancement + modified Enter.
//!
//! Spec: https://sw.kovidgoyal.net/kitty/keyboard-protocol/
//! Product host: `internal/ui/kitty_keyboard.go`.
//!
//! Grok (and other TUIs) probe `CSI ? u` then push flags. Without CSI-u,
//! Shift+Enter / Cmd+Enter collapse to bare CR and submit instead of newline.

/// Disambiguate escape codes (0b1).
pub const DISAMBIGUATE: u16 = 1;
/// Report event types (0b10).
pub const EVENT_TYPES: u16 = 2;
/// Alternate key reports (0b100).
pub const ALTERNATE_KEYS: u16 = 4;
/// Report all keys as escape codes (0b1000).
pub const ALL_KEYS_AS_ESCAPES: u16 = 8;
/// Report associated text (0b10000).
pub const ASSOCIATED_TEXT: u16 = 16;

const STACK_MAX: usize = 16;

/// Progressive-enhancement flags the child app requested.
#[derive(Clone, Debug, Default)]
pub struct KittyKeyboard {
    flags: u16,
    stack: Vec<u16>,
}

impl KittyKeyboard {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn flags(&self) -> u16 {
        self.flags
    }

    /// True when the app asked for disambiguation or full key reporting.
    pub fn active(&self) -> bool {
        self.flags & (DISAMBIGUATE | ALL_KEYS_AS_ESCAPES) != 0
    }

    pub fn apply(&mut self, flags: u16, mode: u16) {
        let mode = if mode < 1 { 1 } else { mode };
        match mode {
            2 => self.flags |= flags,
            3 => self.flags &= !flags,
            _ => self.flags = flags,
        }
    }

    pub fn push(&mut self, flags: u16) {
        if self.stack.len() >= STACK_MAX {
            self.stack.remove(0);
        }
        self.stack.push(self.flags);
        self.flags = flags;
    }

    pub fn pop(&mut self, n: u16) {
        let n = n.max(1);
        for _ in 0..n {
            match self.stack.pop() {
                Some(prev) => self.flags = prev,
                None => {
                    self.flags = 0;
                    return;
                }
            }
        }
    }

    /// Reply to `CSI ? u` / `CSI ? 0 u`.
    pub fn query_reply(&self) -> Vec<u8> {
        format!("\x1b[?{}u", self.flags).into_bytes()
    }
}

/// Kitty modifier parameter: `1 + bitfield` (shift=1, alt=2, ctrl=4, super=8).
pub fn kitty_mods(shift: bool, alt: bool, ctrl: bool, super_key: bool) -> u16 {
    let mut m = 0u16;
    if shift {
        m |= 1;
    }
    if alt {
        m |= 2;
    }
    if ctrl {
        m |= 4;
    }
    if super_key {
        m |= 8;
    }
    1 + m
}

/// `CSI key u` or `CSI key ; mods u`.
pub fn kitty_csi_u(key: u16, mods: u16) -> Vec<u8> {
    if mods <= 1 {
        format!("\x1b[{key}u").into_bytes()
    } else {
        format!("\x1b[{key};{mods}u").into_bytes()
    }
}

/// PTY bytes for Enter with modifiers.
///
/// Plain Enter is CR. When Kitty is active, every modified Enter is CSI-u so
/// Grok can treat Shift / Alt / Cmd as newline. Without a flags push: Alt+Enter
/// stays legacy ESC CR (Grok doctor fallback); Shift / Super / Ctrl still emit
/// CSI-u so apps that parse it without negotiation still get a newline.
pub fn encode_enter(
    shift: bool,
    alt: bool,
    ctrl: bool,
    super_key: bool,
    kitty_active: bool,
) -> Vec<u8> {
    if !shift && !alt && !ctrl && !super_key {
        return vec![b'\r'];
    }
    let mods = kitty_mods(shift, alt, ctrl, super_key);
    if kitty_active {
        return kitty_csi_u(13, mods);
    }
    if alt && !shift && !ctrl && !super_key {
        return vec![0x1b, b'\r'];
    }
    kitty_csi_u(13, mods)
}

/// Classic C0 for Ctrl+letter (`A`=1 … `Z`=26). Space → NUL, `\` → FS.
pub fn encode_ctrl_char(s: &str) -> Option<u8> {
    let mut cs = s.chars();
    let c = cs.next()?.to_ascii_lowercase();
    if cs.next().is_some() {
        return None;
    }
    match c {
        'a'..='z' => Some(c as u8 - b'a' + 1),
        ' ' => Some(0),
        '\\' => Some(0x1c),
        _ => None,
    }
}

/// Modifier bits for a PTY key (Kitty / xterm).
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct KeyMods {
    pub shift: bool,
    pub alt: bool,
    pub ctrl: bool,
    pub super_key: bool,
}

impl KeyMods {
    pub fn any(self) -> bool {
        self.shift || self.alt || self.ctrl || self.super_key
    }

    pub fn param(self) -> u16 {
        kitty_mods(self.shift, self.alt, self.ctrl, self.super_key)
    }
}

/// Arrow CSI final: A=up B=down C=right D=left.
///
/// Bare arrows use SS3 (`ESC O X`) when the app requested cursor keys.
/// Modified arrows stay `CSI 1;mods X` even with Kitty flags — not CSI-u
/// (crossterm only maps the legacy form to Left/Right/Up/Down).
pub fn encode_arrow(dir: u8, app_cursor: bool, m: KeyMods) -> Vec<u8> {
    if dir != b'A' && dir != b'B' && dir != b'C' && dir != b'D' {
        return Vec::new();
    }
    if !m.any() {
        if app_cursor {
            return vec![0x1b, b'O', dir];
        }
        return vec![0x1b, b'[', dir];
    }
    format!("\x1b[1;{}{}", m.param(), dir as char).into_bytes()
}

/// Functional / named keys for alt-screen TUIs (Grok, vim, …).
#[derive(Clone, Copy, Debug)]
pub enum NamedKey {
    Enter,
    Esc,
    Tab,
    Backspace,
    Delete,
    Insert,
    Home,
    End,
    PageUp,
    PageDown,
    ArrowUp,
    ArrowDown,
    ArrowRight,
    ArrowLeft,
    F(u8),
}

/// Encode a named key. Cmd+Left/Right map to Home/End (macOS text-field).
pub fn encode_named(key: NamedKey, app_cursor: bool, kitty_active: bool, m: KeyMods) -> Vec<u8> {
    match key {
        NamedKey::Enter => encode_enter(m.shift, m.alt, m.ctrl, m.super_key, kitty_active),
        NamedKey::Esc => vec![0x1b],
        NamedKey::Tab => {
            if m.shift && !m.alt && !m.ctrl && !m.super_key {
                return b"\x1b[Z".to_vec();
            }
            if m.any() {
                return kitty_csi_u(9, m.param());
            }
            vec![b'\t']
        }
        NamedKey::Backspace => {
            if m.alt && !m.ctrl && !m.super_key && !m.shift {
                return vec![0x1b, 0x7f];
            }
            if m.any() {
                return kitty_csi_u(127, m.param());
            }
            vec![0x7f]
        }
        NamedKey::Delete => {
            if m.any() {
                format!("\x1b[3;{}~", m.param()).into_bytes()
            } else {
                b"\x1b[3~".to_vec()
            }
        }
        NamedKey::Insert => b"\x1b[2~".to_vec(),
        NamedKey::Home => encode_home_end(true, app_cursor, m),
        NamedKey::End => encode_home_end(false, app_cursor, m),
        NamedKey::PageUp => encode_tilde(5, m),
        NamedKey::PageDown => encode_tilde(6, m),
        NamedKey::ArrowUp => encode_arrow(b'A', app_cursor, m),
        NamedKey::ArrowDown => encode_arrow(b'B', app_cursor, m),
        NamedKey::ArrowRight => {
            if m.super_key && !m.alt && !m.ctrl && !m.shift {
                return encode_home_end(false, app_cursor, KeyMods::default());
            }
            encode_arrow(b'C', app_cursor, m)
        }
        NamedKey::ArrowLeft => {
            if m.super_key && !m.alt && !m.ctrl && !m.shift {
                return encode_home_end(true, app_cursor, KeyMods::default());
            }
            encode_arrow(b'D', app_cursor, m)
        }
        NamedKey::F(n) => encode_fn(n),
    }
}

fn encode_home_end(home: bool, app_cursor: bool, m: KeyMods) -> Vec<u8> {
    let letter = if home { b'H' } else { b'F' };
    if !m.any() {
        if app_cursor {
            return vec![0x1b, b'O', letter];
        }
        return vec![0x1b, b'[', letter];
    }
    format!("\x1b[1;{}{}", m.param(), letter as char).into_bytes()
}

fn encode_tilde(n: u8, m: KeyMods) -> Vec<u8> {
    if m.any() {
        format!("\x1b[{n};{}~", m.param()).into_bytes()
    } else {
        format!("\x1b[{n}~").into_bytes()
    }
}

fn encode_fn(n: u8) -> Vec<u8> {
    match n {
        1 => b"\x1bOP".to_vec(),
        2 => b"\x1bOQ".to_vec(),
        3 => b"\x1bOR".to_vec(),
        4 => b"\x1bOS".to_vec(),
        5 => b"\x1b[15~".to_vec(),
        6 => b"\x1b[17~".to_vec(),
        7 => b"\x1b[18~".to_vec(),
        8 => b"\x1b[19~".to_vec(),
        9 => b"\x1b[20~".to_vec(),
        10 => b"\x1b[21~".to_vec(),
        11 => b"\x1b[23~".to_vec(),
        12 => b"\x1b[24~".to_vec(),
        _ => Vec::new(),
    }
}

/// Character with modifiers for the PTY. Bare Alt+letter is left for the host.
pub fn encode_character(s: &str, m: KeyMods) -> Option<Vec<u8>> {
    if m.ctrl && !m.super_key && !m.alt {
        if !m.shift {
            if let Some(b) = encode_ctrl_char(s) {
                return Some(vec![b]);
            }
            return encode_ctrl_punct(s);
        }
        let ch = s.chars().next()?.to_ascii_lowercase();
        if ch.is_ascii_lowercase() {
            return Some(kitty_csi_u(ch as u16, m.param()));
        }
        return None;
    }
    if m.super_key && !m.ctrl && !m.alt {
        // Cmd+Z undo (C0) even without Kitty; other Cmd+letter as CSI-u
        // so Grok sees SUPER (paste ⌘V, settings ⌘,).
        if !m.shift && matches!(s, "z" | "Z") {
            return Some(vec![0x1a]);
        }
        let ch = s.chars().next()?.to_ascii_lowercase();
        if ch.is_ascii_alphabetic() || matches!(ch, ',' | '.' | ';' | '\'' | '/') {
            return Some(kitty_csi_u(ch as u16, m.param()));
        }
        return None;
    }
    if m.alt && !m.ctrl && !m.super_key {
        return None;
    }
    if !m.ctrl && !m.super_key {
        return Some(s.as_bytes().to_vec());
    }
    None
}

/// Ctrl+punctuation that has no C0 (Grok queue / settings / search).
pub fn encode_ctrl_punct(s: &str) -> Option<Vec<u8>> {
    let mut cs = s.chars();
    let c = cs.next()?;
    if cs.next().is_some() {
        return None;
    }
    match c {
        ';' | '\'' | '.' | ',' | '/' => Some(kitty_csi_u(c as u16, kitty_mods(false, false, true, false))),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn plain_enter_is_cr() {
        assert_eq!(encode_enter(false, false, false, false, false), b"\r");
    }

    #[test]
    fn shift_enter_is_csi_u() {
        assert_eq!(
            encode_enter(true, false, false, false, false),
            b"\x1b[13;2u"
        );
    }

    #[test]
    fn alt_enter_legacy_without_kitty() {
        assert_eq!(
            encode_enter(false, true, false, false, false),
            b"\x1b\r"
        );
    }

    #[test]
    fn cmd_enter_is_super_csi_u() {
        assert_eq!(
            encode_enter(false, false, false, true, false),
            b"\x1b[13;9u"
        );
    }

    #[test]
    fn kitty_active_alt_enter_is_csi_u() {
        assert_eq!(
            encode_enter(false, true, false, false, true),
            b"\x1b[13;3u"
        );
    }

    #[test]
    fn query_push_pop() {
        let mut k = KittyKeyboard::new();
        assert_eq!(k.query_reply(), b"\x1b[?0u");
        k.push(DISAMBIGUATE);
        assert!(k.active());
        assert_eq!(k.query_reply(), b"\x1b[?1u");
        k.pop(1);
        assert!(!k.active());
        assert_eq!(k.flags(), 0);
    }

    #[test]
    fn set_or_and_clear() {
        let mut k = KittyKeyboard::new();
        k.apply(DISAMBIGUATE, 1);
        assert_eq!(k.flags(), 1);
        k.apply(EVENT_TYPES, 2);
        assert_eq!(k.flags(), 3);
        k.apply(DISAMBIGUATE, 3);
        assert_eq!(k.flags(), 2);
    }

    #[test]
    fn ctrl_c_is_etx() {
        assert_eq!(encode_ctrl_char("c"), Some(0x03));
        assert_eq!(encode_ctrl_char("C"), Some(0x03));
        assert_eq!(encode_ctrl_char("v"), Some(0x16));
        assert_eq!(encode_ctrl_char("z"), Some(0x1a));
        assert_eq!(encode_ctrl_char(";"), None);
    }

    #[test]
    fn ctrl_semicolon_is_csi_u() {
        assert_eq!(encode_ctrl_punct(";").as_deref(), Some(&b"\x1b[59;5u"[..]));
    }

    fn mods(shift: bool, alt: bool, ctrl: bool, super_key: bool) -> KeyMods {
        KeyMods {
            shift,
            alt,
            ctrl,
            super_key,
        }
    }

    #[test]
    fn opt_left_is_csi_1_3_d() {
        assert_eq!(
            encode_arrow(b'D', false, mods(false, true, false, false)),
            b"\x1b[1;3D"
        );
        assert_eq!(
            encode_arrow(b'D', true, mods(false, false, false, false)),
            b"\x1bOD"
        );
        assert_eq!(
            encode_arrow(b'D', false, mods(false, false, true, false)),
            b"\x1b[1;5D"
        );
        assert_eq!(
            encode_arrow(b'C', false, mods(true, false, false, false)),
            b"\x1b[1;2C"
        );
    }

    #[test]
    fn shift_tab_is_csi_z() {
        let b = encode_named(
            NamedKey::Tab,
            false,
            false,
            mods(true, false, false, false),
        );
        assert_eq!(b, b"\x1b[Z");
    }

    #[test]
    fn opt_backspace_is_meta_del() {
        let b = encode_named(
            NamedKey::Backspace,
            false,
            false,
            mods(false, true, false, false),
        );
        assert_eq!(b, b"\x1b\x7f");
    }

    #[test]
    fn cmd_left_is_home() {
        let b = encode_named(
            NamedKey::ArrowLeft,
            false,
            false,
            mods(false, false, false, true),
        );
        assert_eq!(b, b"\x1b[H");
    }

    #[test]
    fn page_up_plain() {
        assert_eq!(
            encode_named(NamedKey::PageUp, false, false, KeyMods::default()),
            b"\x1b[5~"
        );
    }

    #[test]
    fn f2_is_ss3_q() {
        assert_eq!(
            encode_named(NamedKey::F(2), false, false, KeyMods::default()),
            b"\x1bOQ"
        );
    }

    #[test]
    fn cmd_v_is_super_csi_u() {
        let b = encode_character("v", mods(false, false, false, true)).unwrap();
        assert_eq!(b, b"\x1b[118;9u");
    }
}
