//! Canvas UI GlyphRain defaults (motion/color params for the WGSL rain pass).
//! The actual reveal is per-fragment in `shaders/rain.wgsl` — not labels.

/// Canvas UI DEFAULTS from GlyphRainVanilla.ts (jade body for suzuri chrome).
pub const CELL: f32 = 15.0;
/// Base phase rate — a bit lower so the slow-skewed median crawls.
pub const SPEED: f32 = 0.14;
/// Full mix into per-column speed hash (skewed slow in shader).
pub const SPEED_VARIANCE: f32 = 1.0;
/// Legacy density (stratified activation in shader owns real coverage now).
pub const DENSITY: f32 = 0.4;
/// Trail length — Canvas UI default 0.65; ~20% shorter for chrome.
pub const TRAIL: f32 = 0.52;
pub const GLOW: f32 = 1.75;
pub const MUTATE: f32 = 0.0;
pub const FLICKER: f32 = 0.0;
/// Single layer — multi-scale layers caused cross-column glyph overlap.
pub const LAYERS: f32 = 1.0;

// Theme primary (inkstone jade #00e676). Head is brighter primary — not white.
pub const COLOR: [f32; 3] = [0.0, 0.901_960_8, 0.462_745_1];
pub const HEAD_COLOR: [f32; 3] = [0.15, 1.0, 0.55];
