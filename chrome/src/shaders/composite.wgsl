// Composite chrome over rain: dim title bar + refractive-ish glass panels
// + solid macOS traffic-light dots.

struct FrameUniforms {
    // xy = logical size (css px), zw = framebuffer size
    size: vec4f,
    // time, dpr, panel_count, unused
    misc: vec4f,
}

struct Panel {
    rect: vec4f,   // x y w h logical
    radius: f32,
    kind: f32,
    _pad: vec2f,
}

@group(0) @binding(0) var<uniform> u: FrameUniforms;
@group(0) @binding(1) var rain_tex: texture_2d<f32>;
@group(0) @binding(2) var rain_samp: sampler;
@group(0) @binding(3) var<storage, read> panels: array<Panel>;

struct VsOut {
    @builtin(position) pos: vec4f,
    @location(0) uv: vec2f,
}

@vertex
fn vs(@builtin(vertex_index) vi: u32) -> VsOut {
    var p = array<vec2f, 3>(
        vec2f(-1.0, -1.0),
        vec2f(3.0, -1.0),
        vec2f(-1.0, 3.0),
    );
    var out: VsOut;
    out.pos = vec4f(p[vi], 0.0, 1.0);
    out.uv = p[vi] * 0.5 + 0.5;
    out.uv.y = 1.0 - out.uv.y;
    return out;
}

fn sd_round_box(p: vec2f, b: vec2f, r: f32) -> f32 {
    let q = abs(p) - b + vec2f(r);
    return length(max(q, vec2f(0.0))) + min(max(q.x, q.y), 0.0) - r;
}

fn sample_rain(logical_px: vec2f, refract_off: vec2f) -> vec3f {
    let logical = u.size.xy;
    let uv = (logical_px + refract_off) / logical;
    // flip Y: rain texture is top-left in our rain pass
    let suv = vec2f(clamp(uv.x, 0.0, 1.0), clamp(1.0 - uv.y, 0.0, 1.0));
    return textureSampleLevel(rain_tex, rain_samp, suv, 0.0).rgb;
}

@fragment
fn fs(in: VsOut) -> @location(0) vec4f {
    let logical = u.size.xy;
    let px = in.uv * logical; // top-left origin

    var col = sample_rain(px, vec2f(0.0));

    // Title bar dim strip
    let title_h = 44.0;
    if (px.y < title_h) {
        let bar = vec3f(0.03, 0.07, 0.05);
        col = mix(col, bar, 0.55);
    }

    let n = u32(u.misc.z);
    for (var i = 0u; i < 32u; i++) {
        if (i >= n) { break; }
        let p = panels[i];
        let r = p.rect;
        let center = r.xy + r.zw * 0.5;
        let half = r.zw * 0.5;
        let d = sd_round_box(px - center, half, p.radius);
        let inside = 1.0 - smoothstep(-1.0, 1.0, d);
        if (inside <= 0.001) { continue; }

        // Solid traffic-light kinds (6 close / 7 min / 8 zoom)
        if (p.kind > 5.5) {
            var solid = vec3f(1.0, 0.373, 0.341); // #ff5f57 close
            if (p.kind > 6.5 && p.kind < 7.5) {
                solid = vec3f(0.996, 0.737, 0.180); // #febc2e min
            } else if (p.kind > 7.5) {
                solid = vec3f(0.157, 0.784, 0.251); // #28c840 zoom
            }
            // soft edge + slight inner shade
            let shade = 0.92 + 0.08 * smoothstep(p.radius, 0.0, length(px - center));
            col = mix(col, solid * shade, inside);
            continue;
        }

        // Edge fresnel / rim
        let rim = 1.0 - smoothstep(0.0, 8.0, abs(d));
        // Cheap refraction offset toward center
        let away = normalize(px - center + vec2f(0.001));
        let bend = away * (4.0 + p.kind * 0.5) * (1.0 - smoothstep(-6.0, 0.0, d));
        var glass = sample_rain(px, bend);

        // Tint by kind
        var tint = vec3f(0.08, 0.55, 0.32);
        var tint_a = 0.22;
        var dark = 0.28;
        if (p.kind > 1.5 && p.kind < 2.5) {
            // active chip — brighter jade
            tint = vec3f(0.15, 0.85, 0.5);
            tint_a = 0.4;
            dark = 0.2;
        } else if (p.kind > 2.5) {
            dark = 0.35;
            tint_a = 0.18;
        }

        glass = mix(glass, tint, tint_a);
        glass = mix(glass, vec3f(0.02, 0.04, 0.03), dark);
        // Specular rim
        glass += vec3f(0.75, 1.0, 0.85) * rim * 0.12;

        col = mix(col, glass, inside * 0.92);
        // Soft outer edge
        col = mix(col, glass * 1.05, rim * 0.25 * inside);
    }

    return vec4f(col, 1.0);
}
