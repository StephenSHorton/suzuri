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
    /// Theme **primary** (product `colPrimary` / inkstone jade role).
    /// Borders, selection, rain, self chat bubbles, active chrome.
    pub jade: [f32; 3],
    /// Theme **accent / secondary** (product `colSecondary`).
    /// Highlights, link hover, complementary chrome — derived from primary
    /// unless the user overrides it in settings.
    pub secondary: [f32; 3],
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

/// Default **primary** (inkstone jade `#00e676`).
pub const DEFAULT_PRIMARY: [f32; 3] = [0.0, 0.902, 0.463];

/// Alias — historical name when primary lived in the `accent` prefs field.
pub const DEFAULT_ACCENT: [f32; 3] = DEFAULT_PRIMARY;

/// Preset colors for the settings swatch strip (primary or accent override).
pub const COLOR_PRESETS: &[[f32; 3]] = &[
    [0.0, 0.902, 0.463],   // jade (default primary)
    [0.533, 0.753, 0.816], // nord frost
    [0.741, 0.576, 0.976], // dracula purple
    [0.478, 0.635, 0.969], // tokyo blue
    [0.655, 0.545, 0.980], // charm violet
    [1.0, 0.420, 0.420],   // coral
    [1.0, 0.75, 0.20],     // amber
    [0.30, 0.85, 0.90],    // cyan
];

/// @deprecated use [`COLOR_PRESETS`].
pub const ACCENT_PRESETS: &[[f32; 3]] = COLOR_PRESETS;

/// sRGB channel → linear (WCAG).
fn srgb_to_linear(c: f32) -> f32 {
    let c = c.clamp(0.0, 1.0);
    if c <= 0.04045 {
        c / 12.92
    } else {
        ((c + 0.055) / 1.055).powf(2.4)
    }
}

/// Relative luminance Y ∈ [0,1] (WCAG 2.x).
pub fn relative_luminance(rgb: [f32; 3]) -> f32 {
    let r = srgb_to_linear(rgb[0]);
    let g = srgb_to_linear(rgb[1]);
    let b = srgb_to_linear(rgb[2]);
    0.2126 * r + 0.7152 * g + 0.0722 * b
}

/// Pick black or white text for `bg` using the common WCAG threshold (~Figma/plugin practice):
/// luminance **> 0.179** → black text, else white. Maximizes contrast vs the fill.
pub const TEXT_LUMINANCE_THRESHOLD: f32 = 0.179;

pub fn contrasting_text(bg: [f32; 3]) -> [f32; 3] {
    if relative_luminance(bg) > TEXT_LUMINANCE_THRESHOLD {
        [0.08, 0.08, 0.09] // near-black
    } else {
        [0.96, 0.97, 0.96] // near-white
    }
}

fn mix(a: [f32; 3], b: [f32; 3], t: f32) -> [f32; 3] {
    let t = t.clamp(0.0, 1.0);
    [
        a[0] + (b[0] - a[0]) * t,
        a[1] + (b[1] - a[1]) * t,
        a[2] + (b[2] - a[2]) * t,
    ]
}

/// Derive product-style **secondary / accent** from a primary.
///
/// Catalog pairs (charm violet→gold, nord frost→amber, tokyo blue→purple)
/// are roughly a warm hue shift: +42°, slightly lower sat, lifted value.
pub fn derive_accent(primary: [f32; 3]) -> [f32; 3] {
    let (h, s, v) = rgb_to_hsv(primary);
    let h2 = (h + 42.0).rem_euclid(360.0);
    let s2 = (s * 0.82 + 0.12).clamp(0.28, 0.95);
    let v2 = (v * 0.88 + 0.14).clamp(0.55, 0.98);
    hsv_to_rgb(h2, s2, v2)
}

/// Build a palette from primary + optional accent override.
/// `accent_override = None` → [`derive_accent`].
pub fn from_primary(primary: [f32; 3], accent_override: Option<[f32; 3]>) -> ThemeColors {
    let primary = [
        primary[0].clamp(0.0, 1.0),
        primary[1].clamp(0.0, 1.0),
        primary[2].clamp(0.0, 1.0),
    ];
    let secondary = match accent_override {
        Some(a) => [
            a[0].clamp(0.0, 1.0),
            a[1].clamp(0.0, 1.0),
            a[2].clamp(0.0, 1.0),
        ],
        None => derive_accent(primary),
    };
    // Deep void with a hint of primary hue (readable glass on rain).
    let bg = mix(primary, [0.02, 0.03, 0.03], 0.92);
    let fg = contrasting_text(bg);
    let muted = mix(fg, bg, 0.45);
    ThemeColors {
        bg,
        fg,
        jade: primary,
        secondary,
        muted,
        err: [1.0, 0.32, 0.32],
    }
}

/// Legacy: primary only (accent auto-derived). Prefer [`from_primary`].
pub fn from_accent(primary: [f32; 3]) -> ThemeColors {
    from_primary(primary, None)
}

/// RGB → `#rrggbb`.
pub fn to_hex(rgb: [f32; 3]) -> String {
    let b = |c: f32| (c.clamp(0.0, 1.0) * 255.0).round() as u8;
    format!("#{:02x}{:02x}{:02x}", b(rgb[0]), b(rgb[1]), b(rgb[2]))
}

/// Parse `#rgb` / `#rrggbb` (also bare hex). None if invalid.
pub fn parse_hex(s: &str) -> Option<[f32; 3]> {
    let h = s.trim().trim_start_matches('#');
    let full = if h.len() == 3 {
        let mut o = String::with_capacity(6);
        for c in h.chars() {
            o.push(c);
            o.push(c);
        }
        o
    } else if h.len() == 6 {
        h.to_string()
    } else {
        return None;
    };
    if !full.chars().all(|c| c.is_ascii_hexdigit()) {
        return None;
    }
    let parse = |i: usize| {
        u8::from_str_radix(&full[i..i + 2], 16)
            .ok()
            .map(|v| v as f32 / 255.0)
    };
    Some([parse(0)?, parse(2)?, parse(4)?])
}

/// Rotate hue in HSV by `delta_deg` (degrees), keep S/V.
pub fn rotate_hue(rgb: [f32; 3], delta_deg: f32) -> [f32; 3] {
    let (h, s, v) = rgb_to_hsv(rgb);
    let h2 = (h + delta_deg).rem_euclid(360.0);
    hsv_to_rgb(h2, s, v)
}

fn rgb_to_hsv(rgb: [f32; 3]) -> (f32, f32, f32) {
    let r = rgb[0].clamp(0.0, 1.0);
    let g = rgb[1].clamp(0.0, 1.0);
    let b = rgb[2].clamp(0.0, 1.0);
    let max = r.max(g).max(b);
    let min = r.min(g).min(b);
    let d = max - min;
    let v = max;
    let s = if max < 1e-6 { 0.0 } else { d / max };
    let h = if d < 1e-6 {
        0.0
    } else if (max - r).abs() < 1e-6 {
        60.0 * (((g - b) / d) % 6.0)
    } else if (max - g).abs() < 1e-6 {
        60.0 * (((b - r) / d) + 2.0)
    } else {
        60.0 * (((r - g) / d) + 4.0)
    };
    (h.rem_euclid(360.0), s, v)
}

fn hsv_to_rgb(h: f32, s: f32, v: f32) -> [f32; 3] {
    let c = v * s;
    let x = c * (1.0 - ((h / 60.0) % 2.0 - 1.0).abs());
    let m = v - c;
    let (r1, g1, b1) = if h < 60.0 {
        (c, x, 0.0)
    } else if h < 120.0 {
        (x, c, 0.0)
    } else if h < 180.0 {
        (0.0, c, x)
    } else if h < 240.0 {
        (0.0, x, c)
    } else if h < 300.0 {
        (x, 0.0, c)
    } else {
        (c, 0.0, x)
    };
    [r1 + m, g1 + m, b1 + m]
}

// ── Catalog ─────────────────────────────────────────────────────────────────
// Roles: bg ≈ void, fg ≈ text, jade ≈ primary, secondary ≈ accent, muted ≈ dim.
// Inkstone keeps the existing cell jade-green look (tests + rain defaults).
// Named `secondary` values match product `colSecondary` where catalog aligns.

/// Inkstone — deep green-black terminal, jade primary (#00e676).
pub const INKSTONE: ThemeColors = ThemeColors {
    bg: hex_const(0x05, 0x0a, 0x07),
    fg: hex_const(0xe8, 0xf5, 0xee),
    jade: hex_const(0x00, 0xe6, 0x76),
    secondary: hex_const(0xc4, 0xa3, 0x5a), // soft gold (pair with jade)
    muted: hex_const(0x9a, 0xae, 0xa2),
    err: hex_const(0xff, 0x52, 0x52),
};

/// Nord — arctic blue-greys.
pub const NORD: ThemeColors = ThemeColors {
    bg: hex_const(0x2e, 0x34, 0x40),
    fg: hex_const(0xec, 0xef, 0xf4),
    jade: hex_const(0x88, 0xc0, 0xd0),
    secondary: hex_const(0xeb, 0xcb, 0x8b),
    muted: hex_const(0x8a, 0x96, 0xaa),
    err: hex_const(0xbf, 0x61, 0x6a),
};

/// Dracula — purple base, pink secondary.
pub const DRACULA: ThemeColors = ThemeColors {
    bg: hex_const(0x28, 0x2a, 0x36),
    fg: hex_const(0xf8, 0xf8, 0xf2),
    jade: hex_const(0xbd, 0x93, 0xf9),
    secondary: hex_const(0xff, 0x79, 0xc6),
    muted: hex_const(0x98, 0xa0, 0xc8),
    err: hex_const(0xff, 0x55, 0x55),
};

/// Tokyo Night — night-city blues.
pub const TOKYO_NIGHT: ThemeColors = ThemeColors {
    bg: hex_const(0x1a, 0x1b, 0x26),
    fg: hex_const(0xc0, 0xca, 0xf5),
    jade: hex_const(0x7a, 0xa2, 0xf7),
    secondary: hex_const(0xbb, 0x9a, 0xf7),
    muted: hex_const(0x8a, 0x93, 0xbb),
    err: hex_const(0xf7, 0x76, 0x8e),
};

/// Charm — warm violet primary, soft gold secondary (product charmtone).
pub const CHARM: ThemeColors = ThemeColors {
    bg: hex_const(0x1a, 0x14, 0x18),
    fg: hex_const(0xf3, 0xe8, 0xee),
    jade: hex_const(0xa7, 0x8b, 0xfa),
    secondary: hex_const(0xf0, 0xd9, 0xa8),
    muted: hex_const(0xb0, 0x9e, 0xa8),
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

    #[test]
    fn contrasting_text_uses_luminance_threshold() {
        // Dark fills → near-white text (WCAG / Figma-style 0.179 cut).
        let on_black = contrasting_text([0.0, 0.0, 0.0]);
        assert!(on_black[0] > 0.9);
        // Light fills → near-black text.
        let on_white = contrasting_text([1.0, 1.0, 1.0]);
        assert!(on_white[0] < 0.2);
        // Jade (#00e676) is bright green (high G weight) → black checkmark on swatch.
        assert!(relative_luminance(DEFAULT_ACCENT) > TEXT_LUMINANCE_THRESHOLD);
        let on_jade = contrasting_text(DEFAULT_ACCENT);
        assert!(on_jade[0] < 0.2, "bright jade prefers dark ink");
        // Deep red is below threshold → white ink.
        let on_deep = contrasting_text([0.35, 0.05, 0.05]);
        assert!(on_deep[0] > 0.9);
        // Amber sits well above threshold → black ink.
        let on_amber = contrasting_text([1.0, 0.75, 0.20]);
        assert!(on_amber[0] < 0.2);
    }

    #[test]
    fn from_primary_sets_jade_and_derives_accent() {
        let red = from_primary([1.0, 0.0, 0.0], None);
        assert!((red.jade[0] - 1.0).abs() < 1e-4);
        assert!(red.jade[1] < 0.01);
        // Deep bg → light fg via contrasting_text.
        assert!(red.fg[0] > 0.9);
        // Auto accent is not pure red (hue-shifted).
        assert!(
            (red.secondary[0] - 1.0).abs() > 0.05 || red.secondary[1] > 0.05,
            "derived accent should differ from primary"
        );
        let custom = from_primary([1.0, 0.0, 0.0], Some([0.0, 1.0, 0.0]));
        assert!((custom.secondary[1] - 1.0).abs() < 1e-4);
        assert_eq!(to_hex([0.0, 0.902, 0.463]), "#00e676");
        assert_eq!(parse_hex("#f00"), Some([1.0, 0.0, 0.0]));
        let spun = rotate_hue([1.0, 0.0, 0.0], 120.0);
        assert!(spun[1] > spun[0] && spun[1] > spun[2]);
        // Default primary's auto-accent is stable.
        let d = derive_accent(DEFAULT_PRIMARY);
        assert_eq!(from_primary(DEFAULT_PRIMARY, None).secondary, d);
    }
}
