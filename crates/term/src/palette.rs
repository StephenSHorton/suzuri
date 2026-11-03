//! Go suzuri's 16 themes (internal/chrome/theme.go themeCatalog, ids and
//! labels from internal/config/config.go), copied verbatim: the chrome roles
//! the UI paints with and the shell ANSI-16 table, plus its ANSI map
//! (none / soft / full). The active theme is process-wide: panes resolve
//! colours through it, and a switch asks every pane for a full frame.

use parking_lot::RwLock;

pub type Rgb = (u8, u8, u8);

const fn hex(v: u32) -> Rgb {
    ((v >> 16) as u8, (v >> 8) as u8, v as u8)
}

/// Chrome colour roles (Crush quickStyleOpts roles in Go suzuri).
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Chrome {
    pub void: Rgb,
    pub bar: Rgb,
    pub panel: Rgb,
    pub primary: Rgb,
    pub secondary: Rgb,
    pub on_primary: Rgb,
    pub text: Rgb,
    pub soft: Rgb,
    pub dim: Rgb,
    pub mute: Rgb,
    pub sel: Rgb,
    pub match_: Rgb,
    pub cyan: Rgb,
    pub border: Rgb,
    pub dim_matte: Rgb,
}

#[derive(Debug, Clone, Copy)]
pub enum Ansi {
    Own([Rgb; 16]),
    /// The stock VT table; high contrast brightens a few slots.
    Stock { punch_hc: bool },
}

#[derive(Debug, Clone, Copy)]
pub struct ThemeSpec {
    pub id: &'static str,
    pub label: &'static str,
    pub chrome: Chrome,
    pub ansi: Ansi,
}

impl ThemeSpec {
    /// The theme's own ANSI-16 table (before the ANSI map).
    pub fn ansi16(&self) -> [Rgb; 16] {
        match self.ansi {
            Ansi::Own(t) => t,
            Ansi::Stock { punch_hc } => {
                let mut t = STOCK_ANSI16;
                if punch_hc {
                    t[1] = (255, 80, 80);
                    t[2] = (0, 230, 120);
                    t[4] = (80, 160, 255);
                    t[15] = (255, 255, 255);
                }
                t
            }
        }
    }
}

/// Go suzuri's StockANSI16: the conventional VT palette.
pub const STOCK_ANSI16: [Rgb; 16] = [
    (12, 12, 12),
    (205, 89, 89),
    (80, 180, 120),
    (210, 180, 70),
    (90, 140, 210),
    (180, 100, 180),
    (70, 170, 190),
    (200, 200, 200),
    (100, 100, 100),
    (230, 110, 110),
    (100, 210, 150),
    (230, 210, 100),
    (120, 170, 240),
    (210, 140, 210),
    (100, 200, 210),
    (240, 240, 240),
];

/// How much of the theme reaches the shell's 16 colours (Go's
/// shell_ansi_map): stock table, a 50/50 blend, or the theme's own.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AnsiMap {
    None,
    Soft,
    Full,
}

impl AnsiMap {
    pub fn parse(s: &str) -> Option<AnsiMap> {
        match s.trim().to_ascii_lowercase().as_str() {
            "none" => Some(AnsiMap::None),
            "soft" => Some(AnsiMap::Soft),
            "full" => Some(AnsiMap::Full),
            _ => None,
        }
    }
    pub fn id(self) -> &'static str {
        match self {
            AnsiMap::None => "none",
            AnsiMap::Soft => "soft",
            AnsiMap::Full => "full",
        }
    }
}

/// Go's RemapANSI16.
pub fn remap(theme: &[Rgb; 16], map: AnsiMap) -> [Rgb; 16] {
    match map {
        AnsiMap::None => STOCK_ANSI16,
        AnsiMap::Full => *theme,
        AnsiMap::Soft => {
            let mut out = [(0, 0, 0); 16];
            for i in 0..16 {
                let mix = |a: u8, b: u8| ((a as u32 * 50 + b as u32 * 50) / 100) as u8;
                out[i] = (mix(theme[i].0, STOCK_ANSI16[i].0), mix(theme[i].1, STOCK_ANSI16[i].1), mix(theme[i].2, STOCK_ANSI16[i].2));
            }
            out
        }
    }
}

pub const DEFAULT_THEME: &str = "inkstone";

/// Every theme, in Go suzuri's settings cycle order (config.ThemeIDs).
pub const THEMES: &[ThemeSpec] = &[
    ThemeSpec {
        id: "inkstone",
        label: "Inkstone",
        chrome: Chrome {
            void: hex(0x0c0c10),
            bar: hex(0x141418),
            panel: hex(0x1c1c22),
            primary: hex(0xb8a0c8),
            secondary: hex(0x9a9ab0),
            on_primary: hex(0x0c0c10),
            text: hex(0xe8e6e3),
            soft: hex(0xb0aea8),
            dim: hex(0x787870),
            mute: hex(0x484848),
            sel: hex(0x2a2430),
            match_: hex(0xc8b0d8),
            cyan: hex(0x7a9a9a),
            border: hex(0xb8a0c8),
            dim_matte: hex(0x08080a),
        },
        ansi: Ansi::Own([
            hex(0x0c0c10), hex(0xc07070), hex(0x70a078), hex(0xc0a860),
            hex(0x7090b8), hex(0xa080a8), hex(0x60a0a0), hex(0xb8b6b0),
            hex(0x585858), hex(0xd88888), hex(0x90c098), hex(0xd8c880),
            hex(0x90b0d0), hex(0xc0a0c8), hex(0x88c0c0), hex(0xe8e6e3),
        ]),
    },
    ThemeSpec {
        id: "charmtone",
        label: "Charmtone",
        chrome: Chrome {
            void: hex(0x1a1418),
            bar: hex(0x201a1e),
            panel: hex(0x2a2228),
            primary: hex(0xa78bfa),
            secondary: hex(0xf0d9a8),
            on_primary: hex(0xffffff),
            text: hex(0xf3e8ee),
            soft: hex(0xc9b8c0),
            dim: hex(0x8a7a84),
            mute: hex(0x5a4a54),
            sel: hex(0x3a2e40),
            match_: hex(0xc4b5fd),
            cyan: hex(0x7dd3c0),
            border: hex(0xa78bfa),
            dim_matte: hex(0x100c10),
        },
        ansi: Ansi::Own([
            hex(0x1a1418), hex(0xe8a0a8), hex(0xa6d5a0), hex(0xe8d5a0),
            hex(0xb0a0e8), hex(0xe0b0d0), hex(0x90d0c8), hex(0xd0c4cc),
            hex(0x6a5a64), hex(0xf0b0b8), hex(0xb8e0b0), hex(0xf0e0b8),
            hex(0xc0b0f0), hex(0xe8c0e0), hex(0xa8e0d8), hex(0xf8f0f4),
        ]),
    },
    ThemeSpec {
        id: "high_contrast",
        label: "High contrast",
        chrome: Chrome {
            void: hex(0x000000),
            bar: hex(0x0a0a0a),
            panel: hex(0x141414),
            primary: hex(0x0a5c32),
            secondary: hex(0xffeb3b),
            on_primary: hex(0xe8fff0),
            text: hex(0xffffff),
            soft: hex(0xe0e0e0),
            dim: hex(0xb0b0b0),
            mute: hex(0x707070),
            sel: hex(0x064528),
            match_: hex(0x69f0ae),
            cyan: hex(0x00e5ff),
            border: hex(0x00e676),
            dim_matte: hex(0x000000),
        },
        ansi: Ansi::Stock { punch_hc: true },
    },
    ThemeSpec {
        id: "nord",
        label: "Nord",
        chrome: Chrome {
            void: hex(0x2e3440),
            bar: hex(0x3b4252),
            panel: hex(0x434c5e),
            primary: hex(0x88c0d0),
            secondary: hex(0xebcb8b),
            on_primary: hex(0x2e3440),
            text: hex(0xeceff4),
            soft: hex(0xd8dee9),
            dim: hex(0xa3b1c6),
            mute: hex(0x4c566a),
            sel: hex(0x4c566a),
            match_: hex(0x8fbcbb),
            cyan: hex(0x8fbcbb),
            border: hex(0x88c0d0),
            dim_matte: hex(0x242933),
        },
        ansi: Ansi::Own([
            hex(0x3b4252), hex(0xbf616a), hex(0xa3be8c), hex(0xebcb8b),
            hex(0x81a1c1), hex(0xb48ead), hex(0x88c0d0), hex(0xe5e9f0),
            hex(0x4c566a), hex(0xbf616a), hex(0xa3be8c), hex(0xebcb8b),
            hex(0x81a1c1), hex(0xb48ead), hex(0x8fbcbb), hex(0xeceff4),
        ]),
    },
    ThemeSpec {
        id: "dracula",
        label: "Dracula",
        chrome: Chrome {
            void: hex(0x21222c),
            bar: hex(0x282a36),
            panel: hex(0x343746),
            primary: hex(0xbd93f9),
            secondary: hex(0xff79c6),
            on_primary: hex(0x21222c),
            text: hex(0xf8f8f2),
            soft: hex(0xe2e2dc),
            dim: hex(0xa0a0a0),
            mute: hex(0x6272a4),
            sel: hex(0x44475a),
            match_: hex(0xffb86c),
            cyan: hex(0x8be9fd),
            border: hex(0xbd93f9),
            dim_matte: hex(0x191a21),
        },
        ansi: Ansi::Own([
            hex(0x21222c), hex(0xff5555), hex(0x50fa7b), hex(0xf1fa8c),
            hex(0xbd93f9), hex(0xff79c6), hex(0x8be9fd), hex(0xf8f8f2),
            hex(0x6272a4), hex(0xff6e6e), hex(0x69ff94), hex(0xffffa5),
            hex(0xd6acff), hex(0xff92df), hex(0xa4ffff), hex(0xffffff),
        ]),
    },
    ThemeSpec {
        id: "tokyo_night",
        label: "Tokyo Night",
        chrome: Chrome {
            void: hex(0x1a1b26),
            bar: hex(0x1f2335),
            panel: hex(0x24283b),
            primary: hex(0x7aa2f7),
            secondary: hex(0xbb9af7),
            on_primary: hex(0x1a1b26),
            text: hex(0xc0caf5),
            soft: hex(0xa9b1d6),
            dim: hex(0x565f89),
            mute: hex(0x414868),
            sel: hex(0x3b4261),
            match_: hex(0x7dcfff),
            cyan: hex(0x7dcfff),
            border: hex(0x7aa2f7),
            dim_matte: hex(0x16161e),
        },
        ansi: Ansi::Own([
            hex(0x15161e), hex(0xf7768e), hex(0x9ece6a), hex(0xe0af68),
            hex(0x7aa2f7), hex(0xbb9af7), hex(0x7dcfff), hex(0xa9b1d6),
            hex(0x414868), hex(0xf7768e), hex(0x9ece6a), hex(0xe0af68),
            hex(0x7aa2f7), hex(0xbb9af7), hex(0x7dcfff), hex(0xc0caf5),
        ]),
    },
    ThemeSpec {
        id: "catppuccin",
        label: "Catppuccin",
        chrome: Chrome {
            void: hex(0x1e1e2e),
            bar: hex(0x181825),
            panel: hex(0x313244),
            primary: hex(0xcba6f7),
            secondary: hex(0xf9e2af),
            on_primary: hex(0x1e1e2e),
            text: hex(0xcdd6f4),
            soft: hex(0xbac2de),
            dim: hex(0x6c7086),
            mute: hex(0x45475a),
            sel: hex(0x45475a),
            match_: hex(0x89b4fa),
            cyan: hex(0x89dceb),
            border: hex(0xcba6f7),
            dim_matte: hex(0x11111b),
        },
        ansi: Ansi::Own([
            hex(0x45475a), hex(0xf38ba8), hex(0xa6e3a1), hex(0xf9e2af),
            hex(0x89b4fa), hex(0xf5c2e7), hex(0x94e2d5), hex(0xbac2de),
            hex(0x585b70), hex(0xf38ba8), hex(0xa6e3a1), hex(0xf9e2af),
            hex(0x89b4fa), hex(0xf5c2e7), hex(0x94e2d5), hex(0xcdd6f4),
        ]),
    },
    ThemeSpec {
        id: "gruvbox",
        label: "Gruvbox",
        chrome: Chrome {
            void: hex(0x1d2021),
            bar: hex(0x282828),
            panel: hex(0x3c3836),
            primary: hex(0xd79921),
            secondary: hex(0xfe8019),
            on_primary: hex(0x1d2021),
            text: hex(0xebdbb2),
            soft: hex(0xd5c4a1),
            dim: hex(0xa89984),
            mute: hex(0x665c54),
            sel: hex(0x504945),
            match_: hex(0xfabd2f),
            cyan: hex(0x8ec07c),
            border: hex(0xd79921),
            dim_matte: hex(0x141617),
        },
        ansi: Ansi::Own([
            hex(0x282828), hex(0xcc241d), hex(0x98971a), hex(0xd79921),
            hex(0x458588), hex(0xb16286), hex(0x689d6a), hex(0xa89984),
            hex(0x928374), hex(0xfb4934), hex(0xb8bb26), hex(0xfabd2f),
            hex(0x83a598), hex(0xd3869b), hex(0x8ec07c), hex(0xebdbb2),
        ]),
    },
    ThemeSpec {
        id: "one_dark",
        label: "One Dark",
        chrome: Chrome {
            void: hex(0x21252b),
            bar: hex(0x282c34),
            panel: hex(0x2c313a),
            primary: hex(0x61afef),
            secondary: hex(0xc678dd),
            on_primary: hex(0xffffff),
            text: hex(0xabb2bf),
            soft: hex(0x9da5b4),
            dim: hex(0x5c6370),
            mute: hex(0x3e4451),
            sel: hex(0x3e4451),
            match_: hex(0xe5c07b),
            cyan: hex(0x56b6c2),
            border: hex(0x61afef),
            dim_matte: hex(0x1b1f23),
        },
        ansi: Ansi::Own([
            hex(0x282c34), hex(0xe06c75), hex(0x98c379), hex(0xe5c07b),
            hex(0x61afef), hex(0xc678dd), hex(0x56b6c2), hex(0xabb2bf),
            hex(0x5c6370), hex(0xe06c75), hex(0x98c379), hex(0xe5c07b),
            hex(0x61afef), hex(0xc678dd), hex(0x56b6c2), hex(0xffffff),
        ]),
    },
    ThemeSpec {
        id: "solarized",
        label: "Solarized",
        chrome: Chrome {
            void: hex(0x002b36),
            bar: hex(0x073642),
            panel: hex(0x0a3a45),
            primary: hex(0x268bd2),
            secondary: hex(0xb58900),
            on_primary: hex(0xfdf6e3),
            text: hex(0x839496),
            soft: hex(0x93a1a1),
            dim: hex(0x586e75),
            mute: hex(0x073642),
            sel: hex(0x073642),
            match_: hex(0x2aa198),
            cyan: hex(0x2aa198),
            border: hex(0x268bd2),
            dim_matte: hex(0x001f27),
        },
        ansi: Ansi::Own([
            hex(0x073642), hex(0xdc322f), hex(0x859900), hex(0xb58900),
            hex(0x268bd2), hex(0xd33682), hex(0x2aa198), hex(0xeee8d5),
            hex(0x002b36), hex(0xcb4b16), hex(0x586e75), hex(0x657b83),
            hex(0x839496), hex(0x6c71c4), hex(0x93a1a1), hex(0xfdf6e3),
        ]),
    },
    ThemeSpec {
        id: "rose_pine",
        label: "Rosé Pine",
        chrome: Chrome {
            void: hex(0x191724),
            bar: hex(0x1f1d2e),
            panel: hex(0x26233a),
            primary: hex(0xc4a7e7),
            secondary: hex(0xebbcba),
            on_primary: hex(0x191724),
            text: hex(0xe0def4),
            soft: hex(0x908caa),
            dim: hex(0x6e6a86),
            mute: hex(0x403d52),
            sel: hex(0x403d52),
            match_: hex(0x9ccfd8),
            cyan: hex(0x9ccfd8),
            border: hex(0xc4a7e7),
            dim_matte: hex(0x12101a),
        },
        ansi: Ansi::Own([
            hex(0x26233a), hex(0xeb6f92), hex(0x31748f), hex(0xf6c177),
            hex(0x9ccfd8), hex(0xc4a7e7), hex(0xebbcba), hex(0xe0def4),
            hex(0x6e6a86), hex(0xeb6f92), hex(0x31748f), hex(0xf6c177),
            hex(0x9ccfd8), hex(0xc4a7e7), hex(0xebbcba), hex(0xe0def4),
        ]),
    },
    ThemeSpec {
        id: "kanagawa",
        label: "Kanagawa",
        chrome: Chrome {
            void: hex(0x1f1f28),
            bar: hex(0x2a2a37),
            panel: hex(0x363646),
            primary: hex(0x7e9cd8),
            secondary: hex(0xe6c384),
            on_primary: hex(0xffffff),
            text: hex(0xdcd7ba),
            soft: hex(0xc8c093),
            dim: hex(0x727169),
            mute: hex(0x54546d),
            sel: hex(0x2d4f67),
            match_: hex(0x7aa89f),
            cyan: hex(0x7fb4ca),
            border: hex(0x7e9cd8),
            dim_matte: hex(0x16161d),
        },
        ansi: Ansi::Own([
            hex(0x090618), hex(0xc34043), hex(0x76946a), hex(0xc0a36e),
            hex(0x7e9cd8), hex(0x957fb8), hex(0x6a9589), hex(0xc8c093),
            hex(0x727169), hex(0xe82424), hex(0x98bb6c), hex(0xe6c384),
            hex(0x7fb4ca), hex(0x938aa9), hex(0x7aa89f), hex(0xdcd7ba),
        ]),
    },
    ThemeSpec {
        id: "monokai",
        label: "Monokai",
        chrome: Chrome {
            void: hex(0x1e1f1c),
            bar: hex(0x272822),
            panel: hex(0x3e3d32),
            primary: hex(0xf92672),
            secondary: hex(0xe6db74),
            on_primary: hex(0xffffff),
            text: hex(0xf8f8f2),
            soft: hex(0xcfcfc2),
            dim: hex(0x75715e),
            mute: hex(0x49483e),
            sel: hex(0x49483e),
            match_: hex(0xa6e22e),
            cyan: hex(0x66d9ef),
            border: hex(0xf92672),
            dim_matte: hex(0x141510),
        },
        ansi: Ansi::Own([
            hex(0x272822), hex(0xf92672), hex(0xa6e22e), hex(0xe6db74),
            hex(0x66d9ef), hex(0xae81ff), hex(0xa1efe4), hex(0xf8f8f2),
            hex(0x75715e), hex(0xf92672), hex(0xa6e22e), hex(0xe6db74),
            hex(0x66d9ef), hex(0xae81ff), hex(0xa1efe4), hex(0xf9f8f5),
        ]),
    },
    ThemeSpec {
        id: "forest",
        label: "Forest",
        chrome: Chrome {
            void: hex(0x0d1410),
            bar: hex(0x121c16),
            panel: hex(0x1a2a20),
            primary: hex(0x6abf8a),
            secondary: hex(0xc4a35a),
            on_primary: hex(0x0d1410),
            text: hex(0xdce8e0),
            soft: hex(0xa8c0b0),
            dim: hex(0x6a8878),
            mute: hex(0x3a5044),
            sel: hex(0x24382c),
            match_: hex(0x8fd4a8),
            cyan: hex(0x5aa8a0),
            border: hex(0x6abf8a),
            dim_matte: hex(0x0a100c),
        },
        ansi: Ansi::Own([
            hex(0x0d1410), hex(0xc07070), hex(0x6abf8a), hex(0xc4a35a),
            hex(0x5a90b0), hex(0xa080a8), hex(0x5aa8a0), hex(0xb8c8bc),
            hex(0x3a5044), hex(0xd88888), hex(0x8fd4a8), hex(0xd8c080),
            hex(0x80b0d0), hex(0xc0a0c8), hex(0x80c8c0), hex(0xe8f0ea),
        ]),
    },
    ThemeSpec {
        id: "ocean",
        label: "Ocean",
        chrome: Chrome {
            void: hex(0x0a1218),
            bar: hex(0x0e1a22),
            panel: hex(0x152832),
            primary: hex(0x4ec0e0),
            secondary: hex(0x7ab0e0),
            on_primary: hex(0x0a1218),
            text: hex(0xd8e8f0),
            soft: hex(0xa0b8c8),
            dim: hex(0x608098),
            mute: hex(0x304858),
            sel: hex(0x1c3848),
            match_: hex(0x6ad0f0),
            cyan: hex(0x40d0c0),
            border: hex(0x4ec0e0),
            dim_matte: hex(0x070e14),
        },
        ansi: Ansi::Own([
            hex(0x0a1218), hex(0xe07080), hex(0x60c090), hex(0xe0c070),
            hex(0x5090d0), hex(0xb080d0), hex(0x40d0c0), hex(0xb0c8d8),
            hex(0x304858), hex(0xf090a0), hex(0x80e0b0), hex(0xf0d890),
            hex(0x70b0f0), hex(0xd0a0f0), hex(0x60e8d8), hex(0xe8f4f8),
        ]),
    },
    ThemeSpec {
        id: "amber",
        label: "Amber CRT",
        chrome: Chrome {
            void: hex(0x0c0a00),
            bar: hex(0x141200),
            panel: hex(0x1c1800),
            primary: hex(0xffb000),
            secondary: hex(0xffcc33),
            on_primary: hex(0x0c0a00),
            text: hex(0xffd070),
            soft: hex(0xc8a040),
            dim: hex(0x8a7020),
            mute: hex(0x4a3c10),
            sel: hex(0x3a3000),
            match_: hex(0xffe080),
            cyan: hex(0xe0b040),
            border: hex(0xffb000),
            dim_matte: hex(0x080600),
        },
        ansi: Ansi::Own([
            hex(0x0c0a00), hex(0xcc4040), hex(0x80a040), hex(0xffb000),
            hex(0x8080c0), hex(0xc08040), hex(0xc0a040), hex(0xc8a040),
            hex(0x4a3c10), hex(0xff6060), hex(0xa0c050), hex(0xffd040),
            hex(0xa0a0e0), hex(0xe0a050), hex(0xe0c060), hex(0xffe8a0),
        ]),
    },
];

/// The theme with this id; unknown ids fall back to Inkstone, as in Go.
pub fn theme(id: &str) -> &'static ThemeSpec {
    let id = id.trim().to_ascii_lowercase();
    THEMES.iter().find(|t| t.id == id).unwrap_or(&THEMES[0])
}

pub fn valid_theme(id: &str) -> bool {
    THEMES.iter().any(|t| t.id == id)
}

/// What the engine resolves colours against.
#[derive(Debug, Clone, Copy)]
pub struct Active {
    pub theme: &'static ThemeSpec,
    pub map: AnsiMap,
    pub ansi16: [Rgb; 16],
}

static ACTIVE: RwLock<Option<Active>> = RwLock::new(None);

pub fn active() -> Active {
    if let Some(a) = *ACTIVE.read() {
        return a;
    }
    // Inkstone, full map: what rust-suzuri painted before themes.
    let t = theme(DEFAULT_THEME);
    Active { theme: t, map: AnsiMap::Full, ansi16: t.ansi16() }
}

/// Switch the process-wide theme. Callers redraw their panes.
pub fn set_active(id: &str, map: AnsiMap) -> Active {
    let t = theme(id);
    let a = Active { theme: t, map, ansi16: remap(&t.ansi16(), map) };
    *ACTIVE.write() = Some(a);
    a
}

/// The terminal area's background. Go suzuri on macOS paints the shell on
/// black (its ambient layer); the Windows painter fills the theme void.
pub fn term_bg(c: &Chrome) -> Rgb {
    if cfg!(target_os = "macos") { (0, 0, 0) } else { c.void }
}

/// Cell backgrounds Go leaves unpainted, so the terminal background shows:
/// on macOS anything under 42 per channel (cellBGChromaKey); on Windows
/// black, the void and the dim matte (isTransparentOverlayBG).
pub fn bg_shows_through(rgb: Rgb, c: &Chrome) -> bool {
    if cfg!(target_os = "macos") {
        rgb.0 < 42 && rgb.1 < 42 && rgb.2 < 42
    } else {
        rgb == (0, 0, 0) || rgb == c.void || rgb == c.dim_matte
    }
}

/// Go's brightenBoldRGB: bold ink ~35% toward white.
pub fn brighten_bold((r, g, b): Rgb) -> Rgb {
    let up = |c: u8| (c as u32 + (255 - c as u32) * 90 / 255).min(255) as u8;
    (up(r), up(g), up(b))
}

pub fn near_black((r, g, b): Rgb) -> bool {
    r < 28 && g < 28 && b < 28
}

/// Go's lowContrastRGB: too close to read as a band.
pub fn low_contrast(a: Rgb, b: Rgb) -> bool {
    let d = |x: u8, y: u8| (x as i32 - y as i32).abs();
    d(a.0, b.0) + d(a.1, b.1) + d(a.2, b.2) < 90
}

/// Default foreground (Go: chrome text).
pub fn text() -> Rgb {
    active().theme.chrome.text
}

/// Default background (Go: chrome void, the window fill).
pub fn void() -> Rgb {
    active().theme.chrome.void
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sixteen_themes_in_go_order() {
        let ids: Vec<_> = THEMES.iter().map(|t| t.id).collect();
        assert_eq!(
            ids,
            [
                "inkstone", "charmtone", "high_contrast", "nord", "dracula", "tokyo_night", "catppuccin", "gruvbox", "one_dark",
                "solarized", "rose_pine", "kanagawa", "monokai", "forest", "ocean", "amber"
            ]
        );
    }

    #[test]
    fn spot_values_match_go() {
        assert_eq!(theme("dracula").chrome.primary, (0xbd, 0x93, 0xf9));
        assert_eq!(theme("solarized").ansi16()[8], (0x00, 0x2b, 0x36));
        assert_eq!(theme("amber").chrome.void, (0x0c, 0x0a, 0x00));
        let hc = theme("high_contrast").ansi16();
        assert_eq!(hc[2], (0, 230, 120));
        assert_eq!(hc[3], STOCK_ANSI16[3]);
        assert_eq!(theme("nope").id, "inkstone");
    }

    #[test]
    fn soft_map_blends_half() {
        let t = theme("inkstone").ansi16();
        let s = remap(&t, AnsiMap::Soft);
        // inkstone red #c07070 with stock (205,89,89)
        assert_eq!(s[1], (198, 100, 100));
        assert_eq!(remap(&t, AnsiMap::None), STOCK_ANSI16);
        assert_eq!(remap(&t, AnsiMap::Full), t);
    }
}
