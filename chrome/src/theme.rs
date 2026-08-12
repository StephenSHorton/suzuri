//! Named chrome themes for paints (bg / fg / jade / muted).
//!
//! Cell VT defaults stay on the inkstone consts in [`crate::cells::theme`] so
//! existing ANSI tests keep stable colors. The **renderer / glass chrome**
//! should sample the active palette via [`colors`] using `ChromePrefs.theme`.
//!
//! IDs match the product catalog subset (with hyphenated aliases for
//! `tokyo-night` / `charm`). Unknown ids fall back to inkstone.

/// One chrome paint palette (linear-ish RGB floats in 0..=1).
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct ThemeColors {
    /// Panel / terminal hole background.
    pub bg: [f32; 3],
    /// Primary text / glyphs.
    pub fg: [f32; 3],
    /// Accent (inkstone jade, or theme primary).
    pub jade: [f32; 3],
    /// Secondary / dim labels.
    pub muted: [f32; 3],
    /// Error / destructive accent (ANSI red role).
    pub err: [f32; 3],
}

/// Default theme id (product + cell baseline).
pub const DEFAULT_THEME_ID: &str = "inkstone";

/// Selectable theme ids in cycle order (settings hotkey).
pub const THEME_IDS: &[&str] = &[
    "inkstone",
    "nord",
    "dracula",
    "tokyo-night",
    "charm",
];

/// Hex `#rrggbb` → RGB floats.
pub fn hex_rgb(hex: &str) -> [f32; 3] {
    let h = hex.trim().trim_start_matches('#');
    if h.len() < 6 {
        return [0.0, 0.0, 0.0];
    }
    let parse = |i: usize| {
        u8::from_str_radix(&h[i..i + 2], 16)
            .map(|b| b as f32 / 255.0)
            .unwrap_or(0.0)
    };
    [parse(0), parse(2), parse(4)]
}

/// Canonical id for storage / display (aliases folded; unknown → inkstone).
pub fn normalize_id(id: &str) -> &'static str {
    let t = id.trim();
    let lower = t.to_ascii_lowercase();
    match lower.as_str() {
        "inkstone" | "" => "inkstone",
        "nord" => "nord",
        "dracula" => "dracula",
        "tokyo-night" | "tokyo_night" | "tokyonight" => "tokyo-night",
        "charm" | "charmtone" => "charm",
        _ => {
            // Accept exact known ids if casing differed.
            for known in THEME_IDS {
                if known.eq_ignore_ascii_case(t) {
                    return *known;
                }
            }
            DEFAULT_THEME_ID
        }
    }
}

/// Whether `id` is a known theme (after alias fold).
pub fn is_known(id: &str) -> bool {
    let n = normalize_id(id);
    // normalize_id maps unknown → inkstone; distinguish true inkstone input.
    let lower = id.trim().to_ascii_lowercase();
    matches!(
        lower.as_str(),
        "inkstone"
            | ""
            | "nord"
            | "dracula"
            | "tokyo-night"
            | "tokyo_night"
            | "tokyonight"
            | "charm"
            | "charmtone"
    ) || n != DEFAULT_THEME_ID
}

/// Human label for settings UI.
pub fn label(id: &str) -> &'static str {
    match normalize_id(id) {
        "nord" => "Nord",
        "dracula" => "Dracula",
        "tokyo-night" => "Tokyo Night",
        "charm" => "Charm",
        _ => "Inkstone",
    }
}

/// Next theme id in cycle order (wraps).
pub fn cycle_next(id: &str) -> &'static str {
    let cur = normalize_id(id);
    let idx = THEME_IDS.iter().position(|&t| t == cur).unwrap_or(0);
    THEME_IDS[(idx + 1) % THEME_IDS.len()]
}

/// Previous theme id in cycle order (wraps).
pub fn cycle_prev(id: &str) -> &'static str {
    let cur = normalize_id(id);
    let idx = THEME_IDS.iter().position(|&t| t == cur).unwrap_or(0);
    THEME_IDS[(idx + THEME_IDS.len() - 1) % THEME_IDS.len()]
}

/// Palette for a theme id (unknown → inkstone).
pub fn colors(id: &str) -> ThemeColors {
    match normalize_id(id) {
        "nord" => NORD,
        "dracula" => DRACULA,
        "tokyo-night" => TOKYO_NIGHT,
        "charm" => CHARM,
        _ => INKSTONE,
    }
}

// ── Catalog ─────────────────────────────────────────────────────────────────
// Roles: bg ≈ void/dimMatte, fg ≈ text, jade ≈ primary/accent, muted ≈ dim/mute.
// Inkstone keeps the existing cell jade-green look (tests + rain defaults).

/// Inkstone — deep green-black terminal, jade accent (#00e676).
pub const INKSTONE: ThemeColors = ThemeColors {
    bg: hex_const(0x05, 0x0a, 0x07),
    fg: hex_const(0xe8, 0xf5, 0xee),
    jade: hex_const(0x00, 0xe6, 0x76),
    muted: hex_const(0x6b, 0x7c, 0x72),
    err: hex_const(0xff, 0x52, 0x52),
};

/// Nord — arctic blue-greys.
pub const NORD: ThemeColors = ThemeColors {
    bg: hex_const(0x2e, 0x34, 0x40),
    fg: hex_const(0xec, 0xef, 0xf4),
    jade: hex_const(0x88, 0xc0, 0xd0),
    muted: hex_const(0x4c, 0x56, 0x6a),
    err: hex_const(0xbf, 0x61, 0x6a),
};

/// Dracula — purple base, pink/green accents.
pub const DRACULA: ThemeColors = ThemeColors {
    bg: hex_const(0x28, 0x2a, 0x36),
    fg: hex_const(0xf8, 0xf8, 0xf2),
    jade: hex_const(0xbd, 0x93, 0xf9),
    muted: hex_const(0x62, 0x72, 0xa4),
    err: hex_const(0xff, 0x55, 0x55),
};

/// Tokyo Night — night-city blues.
pub const TOKYO_NIGHT: ThemeColors = ThemeColors {
    bg: hex_const(0x1a, 0x1b, 0x26),
    fg: hex_const(0xc0, 0xca, 0xf5),
    jade: hex_const(0x7a, 0xa2, 0xf7),
    muted: hex_const(0x56, 0x5f, 0x89),
    err: hex_const(0xf7, 0x76, 0x8e),
};

/// Charm — warm violet (product charmtone).
pub const CHARM: ThemeColors = ThemeColors {
    bg: hex_const(0x1a, 0x14, 0x18),
    fg: hex_const(0xf3, 0xe8, 0xee),
    jade: hex_const(0xa7, 0x8b, 0xfa),
    muted: hex_const(0x8a, 0x7a, 0x84),
    err: hex_const(0xe8, 0xa0, 0xa8),
};

const fn hex_const(r: u8, g: u8, b: u8) -> [f32; 3] {
    [r as f32 / 255.0, g as f32 / 255.0, b as f32 / 255.0]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_and_ids() {
        assert_eq!(DEFAULT_THEME_ID, "inkstone");
        assert_eq!(THEME_IDS.len(), 5);
        assert_eq!(normalize_id(""), "inkstone");
        assert_eq!(normalize_id("unknown-xyz"), "inkstone");
    }

    #[test]
    fn aliases_fold() {
        assert_eq!(normalize_id("tokyo_night"), "tokyo-night");
        assert_eq!(normalize_id("Tokyo-Night"), "tokyo-night");
        assert_eq!(normalize_id("charmtone"), "charm");
        assert_eq!(normalize_id("CHARM"), "charm");
    }

    #[test]
    fn cycle_wraps() {
        assert_eq!(cycle_next("inkstone"), "nord");
        assert_eq!(cycle_next("charm"), "inkstone");
        assert_eq!(cycle_prev("inkstone"), "charm");
        assert_eq!(cycle_prev("nord"), "inkstone");
    }

    #[test]
    fn inkstone_matches_legacy_cell_consts() {
        // Keep parity with `cells::theme` so VT defaults stay stable.
        let c = colors("inkstone");
        assert!((c.bg[1] - 0.039_215_687).abs() < 1e-5);
        assert!((c.jade[1] - 0.901_960_8).abs() < 1e-5);
        assert!((c.fg[0] - 0.909_803_9).abs() < 1e-5);
    }

    #[test]
    fn palettes_distinct() {
        let a = colors("inkstone");
        let b = colors("nord");
        let c = colors("dracula");
        assert_ne!(a.bg, b.bg);
        assert_ne!(b.jade, c.jade);
        assert_ne!(colors("tokyo-night").fg, colors("charm").fg);
    }

    #[test]
    fn hex_rgb_parses() {
        assert_eq!(hex_rgb("#00e676")[1], 230.0 / 255.0);
        assert_eq!(hex_rgb("282a36")[0], 40.0 / 255.0);
    }

    #[test]
    fn labels() {
        assert_eq!(label("nord"), "Nord");
        assert_eq!(label("tokyo_night"), "Tokyo Night");
        assert_eq!(label("nope"), "Inkstone");
    }
}
