//! How the desktop notice cards (rust-toast) look: Astryx's Neutral theme
//! in dark mode, the same as the webview chrome. The hex values are the
//! dark half of each `light-dark()` in
//! ui/node_modules/@astryxdesign/theme-neutral/dist/theme.css (the `:scope`
//! block); retune here after a theme upgrade. rust-toast has no font
//! setting, so the cards keep its own face (Segoe UI / the system font),
//! not Neutral's Figtree.

use toast::config::mix;
use toast::{Accents, Config, Palette, Scheme, hex};

/// --radius-container: 0.75rem at a 16 px root.
const CORNER_RADIUS: f64 = 12.0;

/// The cards' palette.
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

/// The app-level toast setup: suzuri's name, Neutral dark, solid cards
/// (Neutral's surfaces are opaque, so no glass), visible to screen capture.
pub fn config() -> Config {
    Config {
        app_name: "suzuri".into(),
        dark: palette(),
        scheme: Scheme::Dark,
        corner_radius: CORNER_RADIUS,
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

#[cfg(test)]
mod tests {
    use super::*;

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
}
