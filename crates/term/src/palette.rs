//! The terminal's colours: one process-wide palette that panes resolve
//! colours through. The app's UI derives it from the active Astryx theme
//! (ui/theme_palette.js) and hands it over (appearance_set in the app);
//! until then, and in processes without a UI (`suzuri notify`), it is
//! [`NEUTRAL_DARK`]. A change asks every pane for a full frame (the engines
//! compare [`Active::generation`]).

use parking_lot::RwLock;

pub type Rgb = (u8, u8, u8);

const fn hex(v: u32) -> Rgb {
    ((v >> 16) as u8, (v >> 8) as u8, v as u8)
}

/// What the terminal is painted with.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Palette {
    /// A dark background: the paint rules below lean on it (bold goes
    /// toward white on dark, toward black on light).
    pub dark: bool,
    /// The default background (the pane's surface).
    pub bg: Rgb,
    /// The default ink.
    pub fg: Rgb,
    /// Reverse video's default background.
    pub panel: Rgb,
    /// The theme's accent (block marks).
    pub accent: Rgb,
    /// ANSI 0-15.
    pub ansi16: [Rgb; 16],
}

/// Astryx Neutral in dark mode: what ui/theme_palette.js derives from
/// @astryxdesign/theme-neutral's dark tokens (ui/test/theme_palette.test.mjs
/// checks the two agree).
pub const NEUTRAL_DARK: Palette = Palette {
    dark: true,
    bg: hex(0x1b1b1b),
    fg: hex(0xffffff),
    panel: hex(0x262626),
    accent: hex(0xf1f1f1),
    ansi16: [
        hex(0x303030), hex(0xfd736d), hex(0x71b972), hex(0xc79f18),
        hex(0x67a7f8), hex(0xd17fe4), hex(0x51b4cb), hex(0x9e9e9e),
        hex(0x6c6c6c), hex(0xffaba3), hex(0x96d896), hex(0xe7c04b),
        hex(0x9ec7fd), hex(0xeba6fb), hex(0x7dd3e8), hex(0xffffff),
    ],
};

/// The palette in force and its generation (bumped on every change, so a
/// pane knows to repaint everything).
#[derive(Debug, Clone, Copy)]
pub struct Active {
    pub generation: u64,
    pub pal: Palette,
}

static ACTIVE: RwLock<Active> = RwLock::new(Active { generation: 0, pal: NEUTRAL_DARK });

pub fn active() -> Active {
    *ACTIVE.read()
}

/// Switch the process-wide palette. True when it changed; callers then
/// redraw their panes.
pub fn set_active(p: Palette) -> bool {
    swap(&mut ACTIVE.write(), p)
}

fn swap(a: &mut Active, p: Palette) -> bool {
    if a.pal == p {
        return false;
    }
    a.pal = p;
    a.generation += 1;
    true
}

/// The terminal area's background.
pub fn term_bg(p: &Palette) -> Rgb {
    p.bg
}

/// Cell backgrounds left unpainted, so the terminal background shows: the
/// background itself, and on a dark one what Go suzuri treated as its
/// ambient layer (macOS: anything under 42 per channel, cellBGChromaKey;
/// Windows: black, isTransparentOverlayBG). On a light background an
/// explicit black band stays black.
pub fn bg_shows_through(rgb: Rgb, p: &Palette) -> bool {
    if rgb == p.bg {
        return true;
    }
    if !p.dark {
        return false;
    }
    if cfg!(target_os = "macos") { rgb.0 < 42 && rgb.1 < 42 && rgb.2 < 42 } else { rgb == (0, 0, 0) }
}

/// Bold ink: Go's brightenBoldRGB (~35% toward white) on a dark
/// background; the same amount toward black on a light one.
pub fn bold_ink(c: Rgb, p: &Palette) -> Rgb {
    if p.dark { brighten_bold(c) } else { darken_bold(c) }
}

/// Faint ink: Go's dimRGB on a dark background; on a light one, 45% of the
/// way to the background (dimming toward black would read as bolder).
pub fn faint_ink(c: Rgb, p: &Palette) -> Rgb {
    if p.dark {
        dim_faint(c)
    } else {
        let m = |a: u8, b: u8| ((a as u32 * 55 + b as u32 * 45) / 100) as u8;
        (m(c.0, p.bg.0), m(c.1, p.bg.1), m(c.2, p.bg.2))
    }
}

/// Go's brightenBoldRGB: bold ink ~35% toward white.
pub fn brighten_bold((r, g, b): Rgb) -> Rgb {
    let up = |c: u8| (c as u32 + (255 - c as u32) * 90 / 255).min(255) as u8;
    (up(r), up(g), up(b))
}

/// brighten_bold's mirror: ~35% toward black.
pub fn darken_bold((r, g, b): Rgb) -> Rgb {
    let down = |c: u8| (c as u32 - c as u32 * 90 / 255) as u8;
    (down(r), down(g), down(b))
}

/// Go's dimRGB: 55%, never under 12.
pub fn dim_faint((r, g, b): Rgb) -> Rgb {
    let d = |c: u8| ((c as u32 * 55 / 100).max(12)) as u8;
    (d(r), d(g), d(b))
}

pub fn near_black((r, g, b): Rgb) -> bool {
    r < 28 && g < 28 && b < 28
}

/// Go's lowContrastRGB: too close to read as a band.
pub fn low_contrast(a: Rgb, b: Rgb) -> bool {
    let d = |x: u8, y: u8| (x as i32 - y as i32).abs();
    d(a.0, b.0) + d(a.1, b.1) + d(a.2, b.2) < 90
}

/// Default foreground.
pub fn text() -> Rgb {
    active().pal.fg
}

/// Default background.
pub fn void() -> Rgb {
    active().pal.bg
}

/// xterm 256-colour table entry; 0..16 come from `ansi16`.
pub fn xterm256(ansi16: &[Rgb; 16], i: u8) -> Rgb {
    match i {
        0..=15 => ansi16[i as usize],
        16..=231 => {
            let i = i - 16;
            let step = |v: u8| if v == 0 { 0 } else { 55 + v * 40 };
            (step(i / 36), step((i / 6) % 6), step(i % 6))
        }
        _ => {
            let v = 8 + (i - 232) * 10;
            (v, v, v)
        }
    }
}

/// `#rrggbb` (or `rrggbb`) to a colour; None for anything else.
pub fn parse_hex(s: &str) -> Option<Rgb> {
    let t = s.trim();
    let t = t.strip_prefix('#').unwrap_or(t);
    if t.len() != 6 || !t.bytes().all(|b| b.is_ascii_hexdigit()) {
        return None;
    }
    u32::from_str_radix(t, 16).ok().map(hex)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn faint_and_bold_match_go_on_dark() {
        assert_eq!(dim_faint((200, 100, 10)), (110, 55, 12));
        assert_eq!(brighten_bold((0, 128, 255)), (90, 172, 255));
        assert_eq!(bold_ink((0, 128, 255), &NEUTRAL_DARK), (90, 172, 255));
        assert_eq!(faint_ink((200, 100, 10), &NEUTRAL_DARK), (110, 55, 12));
    }

    #[test]
    fn light_backgrounds_bold_darker_and_faint_toward_the_background() {
        let light = Palette { dark: false, bg: (240, 240, 240), ..NEUTRAL_DARK };
        assert_eq!(bold_ink((255, 128, 0), &light), (165, 83, 0));
        assert_eq!(faint_ink((0, 0, 0), &light), (108, 108, 108));
    }

    #[test]
    fn only_dark_backgrounds_erase_black_bands() {
        assert!(bg_shows_through(NEUTRAL_DARK.bg, &NEUTRAL_DARK));
        assert!(bg_shows_through((0, 0, 0), &NEUTRAL_DARK));
        let light = Palette { dark: false, bg: (240, 240, 240), ..NEUTRAL_DARK };
        assert!(bg_shows_through((240, 240, 240), &light));
        assert!(!bg_shows_through((0, 0, 0), &light));
    }

    #[test]
    fn set_active_bumps_the_generation_only_on_change() {
        // On a local Active: the process-wide one is shared with the
        // engine tests running alongside.
        let mut a = Active { generation: 0, pal: NEUTRAL_DARK };
        assert!(!swap(&mut a, NEUTRAL_DARK));
        assert_eq!(a.generation, 0);
        let other = Palette { accent: (1, 2, 3), ..NEUTRAL_DARK };
        assert!(swap(&mut a, other));
        assert!(!swap(&mut a, other));
        assert_eq!(a.generation, 1);
        assert_eq!(a.pal.accent, (1, 2, 3));
    }

    #[test]
    fn parses_hex() {
        assert_eq!(parse_hex("#1b1b1b"), Some((0x1b, 0x1b, 0x1b)));
        assert_eq!(parse_hex("FFAEA7"), Some((0xff, 0xae, 0xa7)));
        assert_eq!(parse_hex("#fff"), None);
        assert_eq!(parse_hex("rgb(1,2,3)"), None);
    }
}
