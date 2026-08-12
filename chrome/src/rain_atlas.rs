//! Glyph atlas for Canvas UI GlyphRain — same charset + layout idea as
//! `buildAtlas()` in GlyphRainVanilla.ts (64px cells, √N grid, white on clear).

use glyphon::{
    Attrs, Buffer, Color, Family, FontSystem, Metrics, Shaping, SwashCache,
};
use wgpu::util::DeviceExt;

/// Canvas UI GlyphRain default charset.
pub const CHARSET: &str =
    "ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ0123456789Z*+-<>¦=:.";

const CELL_PX: u32 = 64;

pub struct RainAtlas {
    pub texture: wgpu::Texture,
    pub view: wgpu::TextureView,
    pub count: f32,
    pub grid: f32,
}

impl RainAtlas {
    pub fn build(device: &wgpu::Device, queue: &wgpu::Queue) -> Self {
        let glyphs: Vec<char> = {
            let mut v = Vec::new();
            let mut seen = std::collections::HashSet::new();
            for ch in CHARSET.chars() {
                if ch.is_whitespace() {
                    continue;
                }
                if seen.insert(ch) {
                    v.push(ch);
                }
            }
            if v.is_empty() {
                v.extend(['0', '1']);
            }
            v
        };
        let count = glyphs.len() as u32;
        let grid = ((count as f32).sqrt().ceil() as u32).max(1);
        let width = grid * CELL_PX;
        let height = grid * CELL_PX;
        let mut pixels = vec![0u8; (width * height * 4) as usize];

        let mut font_system = FontSystem::new();
        let mut swash = SwashCache::new();
        let font_px = (CELL_PX as f32 * 0.72).round();
        let metrics = Metrics::new(font_px, CELL_PX as f32);

        for (i, ch) in glyphs.iter().enumerate() {
            let gx = (i as u32) % grid;
            let gy = (i as u32) / grid;
            let origin_x = (gx * CELL_PX) as i32;
            let origin_y = (gy * CELL_PX) as i32;

            let mut buffer = Buffer::new(&mut font_system, metrics);
            buffer.set_size(
                &mut font_system,
                Some(CELL_PX as f32),
                Some(CELL_PX as f32),
            );
            // Prefer CJK face for half-width katakana (macOS).
            #[cfg(target_os = "macos")]
            let attrs = Attrs::new().family(Family::Name("Hiragino Sans"));
            #[cfg(not(target_os = "macos"))]
            let attrs = Attrs::new().family(Family::Monospace);

            buffer.set_text(
                &mut font_system,
                &ch.to_string(),
                attrs,
                Shaping::Advanced,
            );
            buffer.shape_until_scroll(&mut font_system, false);

            // Center-ish draw into the cell by offsetting the draw callback.
            // Canvas UI uses textAlign center / middle at cell center.
            let cx = CELL_PX as f32 * 0.5;
            let cy = CELL_PX as f32 * 0.5;
            // Measure rough advance via first run
            let mut draw_dx = 0i32;
            let mut draw_dy = 0i32;
            if let Some(run) = buffer.layout_runs().next() {
                if let Some(g) = run.glyphs.first() {
                    // place glyph so its center sits near cell center
                    draw_dx = (cx - g.x - g.w * 0.5) as i32;
                    draw_dy = (cy - run.line_y) as i32;
                }
            }

            buffer.draw(
                &mut font_system,
                &mut swash,
                Color::rgb(255, 255, 255),
                |x, y, w, h, color| {
                    let a = color.a();
                    if a == 0 {
                        return;
                    }
                    for oy in 0..h as i32 {
                        for ox in 0..w as i32 {
                            let px = origin_x + x + ox + draw_dx;
                            let py = origin_y + y + oy + draw_dy;
                            if px < 0 || py < 0 {
                                continue;
                            }
                            let px = px as u32;
                            let py = py as u32;
                            if px >= width || py >= height {
                                continue;
                            }
                            let idx = ((py * width + px) * 4) as usize;
                            // Max-blend white alpha into atlas
                            let na = a;
                            if na > pixels[idx + 3] {
                                pixels[idx] = 255;
                                pixels[idx + 1] = 255;
                                pixels[idx + 2] = 255;
                                pixels[idx + 3] = na;
                            }
                        }
                    }
                },
            );
        }

        let texture = device.create_texture_with_data(
            queue,
            &wgpu::TextureDescriptor {
                label: Some("rain glyph atlas"),
                size: wgpu::Extent3d {
                    width,
                    height,
                    depth_or_array_layers: 1,
                },
                mip_level_count: 1,
                sample_count: 1,
                dimension: wgpu::TextureDimension::D2,
                format: wgpu::TextureFormat::Rgba8Unorm,
                usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
                view_formats: &[],
            },
            wgpu::util::TextureDataOrder::LayerMajor,
            &pixels,
        );
        let view = texture.create_view(&wgpu::TextureViewDescriptor::default());

        Self {
            texture,
            view,
            count: count as f32,
            grid: grid as f32,
        }
    }
}
