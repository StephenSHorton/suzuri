//! How the desktop notice cards (rust-toast) look: the active Astryx theme,
//! as the webview chrome. The UI resolves the theme's tokens and sends the
//! card colours over (appearance_set in main.rs, [`config_for`]); until it
//! does, the cards are Neutral in dark mode ([`config`]), whose hex values
//! are the dark half of each `light-dark()` in
//! ui/node_modules/@astryxdesign/theme-neutral/dist/theme.css (the `:scope`
//! block); retune here after a theme upgrade. rust-toast has no font
//! setting, so the cards keep its own face (Segoe UI / the system font),
//! not the theme's.

use serde::Deserialize;
use toast::config::mix;
use toast::{Accents, Color, Config, Palette, Scheme, hex, rgb};

/// --radius-container: 0.75rem at a 16 px root.
const CORNER_RADIUS: f64 = 12.0;

/// The cards' palette before the UI sends the theme's.
pub fn palette() -> Palette {
    // --color-background-surface (a card on the body).
    let background = hex("#262626");
    // --color-text-primary.
    let foreground = hex("#ffffff");
    Palette {
        background,
        foreground,
        // --color-text-secondary.
        muted: hex("#9e9e9e"),
        // --color-border is #ffffff1A (white at 10%); toast colours are
        // opaque, so it is composited over the surface here (#3c3c3c).
        border: mix(background, foreground, 0x1A as f64 / 255.0),
        accent: Accents {
            // --color-accent: the stripe of a plain card, as the old
            // webview cards had.
            info: hex("#f1f1f1"),
            // --color-success, --color-warning (one value in both modes),
            // --color-error.
            success: hex("#64af4c"),
            warn: hex("#ffce2f"),
            error: hex("#ff705d"),
        },
        // The first button: Astryx's primary Button is --color-accent with
        // --color-on-accent text.
        primary: hex("#f1f1f1"),
        on_primary: hex("#111111"),
        // faint, hover, button, primary_hover and tint are derived by
        // rust-toast from the five above (Palette::resolved).
        ..Palette::default()
    }
}

/// The app-level toast setup: suzuri's name, solid cards (Astryx's
/// surfaces are opaque, so no glass), visible to screen capture.
fn base(palette: Palette, dark: bool, corner_radius: f64) -> Config {
    let (light, dark_pal, scheme) = if dark { (Palette::default(), palette, Scheme::Dark) } else { (palette, Palette::default(), Scheme::Light) };
    Config {
        app_name: "suzuri".into(),
        light,
        dark: dark_pal,
        scheme,
        corner_radius,
        glass: 1.0,
        // Shown in screenshots and recordings, as the old webview cards were
        // (rust-toast hides them by default).
        capture_exclude: false,
        // No coloured bar down the left edge (Stephen, 2026-11-10): the
        // icon alone carries the kind's colour.
        stripe: false,
        ..Config::default()
    }
}

/// The startup setup: Neutral dark.
pub fn config() -> Config {
    base(palette(), true, CORNER_RADIUS)
}

/// The card colours ui/theme_palette.js resolves from the active theme
/// (its `card`), all opaque `#rrggbb`.
#[derive(Debug, Clone, Deserialize)]
pub struct CardLook {
    pub surface: String,
    pub text: String,
    pub muted: String,
    pub border: String,
    pub accent: String,
    pub on_accent: String,
    pub success: String,
    pub warning: String,
    pub error: String,
    /// --radius-container in px.
    pub radius: f64,
}

fn color(name: &str, s: &str) -> Result<Color, String> {
    let (r, g, b) = rsz_term::palette::parse_hex(s).ok_or_else(|| format!("card {name}: not #rrggbb: {s:?}"))?;
    Ok(rgb(r, g, b))
}

/// The setup for a theme's card colours; `dark` picks the scheme (the
/// theme's resolved background, so a light theme gets light cards).
pub fn config_for(look: &CardLook, dark: bool) -> Result<Config, String> {
    let p = Palette {
        background: color("surface", &look.surface)?,
        foreground: color("text", &look.text)?,
        muted: color("muted", &look.muted)?,
        border: color("border", &look.border)?,
        accent: Accents {
            info: color("accent", &look.accent)?,
            success: color("success", &look.success)?,
            warn: color("warning", &look.warning)?,
            error: color("error", &look.error)?,
        },
        primary: color("accent", &look.accent)?,
        on_primary: color("on_accent", &look.on_accent)?,
        ..Palette::default()
    };
    let radius = if look.radius.is_finite() { look.radius.clamp(0.0, 32.0) } else { CORNER_RADIUS };
    Ok(base(p, dark, radius))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn neutral_light() -> CardLook {
        CardLook {
            surface: "#ffffff".into(),
            text: "#000000".into(),
            muted: "#474747".into(),
            border: "#ebebeb".into(),
            accent: "#1b1b1b".into(),
            on_accent: "#ffffff".into(),
            success: "#198100".into(),
            warning: "#ffce2f".into(),
            error: "#c9303a".into(),
            radius: 12.0,
        }
    }

    #[test]
    fn border_is_white_at_ten_percent_over_the_surface() {
        assert_eq!(palette().border.to_string(), "#3c3c3c");
    }

    #[test]
    fn config_is_solid_neutral_dark() {
        let c = config().normalized();
        assert_eq!(c.app_name, "suzuri");
        assert_eq!(c.scheme, Scheme::Dark);
        assert_eq!(c.corner_radius, 12.0);
        assert!(c.glass_alpha().is_none());
        assert!(!c.capture_exclude);
        assert_eq!(c.dark.background.to_string(), "#262626");
        assert_eq!(c.dark.on_primary.to_string(), "#111111");
    }

    #[test]
    fn a_light_theme_gets_light_cards() {
        let c = config_for(&neutral_light(), false).unwrap().normalized();
        assert_eq!(c.scheme, Scheme::Light);
        assert_eq!(c.light.background.to_string(), "#ffffff");
        assert_eq!(c.light.foreground.to_string(), "#000000");
        assert_eq!(c.light.accent.error.to_string(), "#c9303a");
        assert_eq!(c.light.on_primary.to_string(), "#ffffff");
        assert!(!c.capture_exclude);
        assert!(!c.stripe);
    }

    #[test]
    fn the_theme_radius_is_used_and_bad_colours_are_errors() {
        let mut l = neutral_light();
        l.radius = 0.0;
        assert_eq!(config_for(&l, true).unwrap().corner_radius, 0.0);
        assert_eq!(config_for(&l, true).unwrap().scheme, Scheme::Dark);
        l.error = "red".into();
        assert!(config_for(&l, true).unwrap_err().contains("error"));
    }
}
