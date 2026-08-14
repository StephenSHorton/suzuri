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
}
