// Canvas UI GlyphRain — per-fragment reveal (NOT discrete falling labels).
// Port of DavidHDev/canvas-ui GlyphRainVanilla.ts FRAG rain path.
//
// Glyphs sit on a fixed grid. Brightness is continuous in y:
//   phase = fract(yn + T)
//   b     = trail / (phase * 22) − 0.04   ← smooth falloff
//   head  = 1 − smoothstep(0, cellYn*1.2, phase)  ← bright reveal line
// So the head is a horizontal band that glides down the column while
// characters stay put (only their alpha/brightness changes).

struct RainUniforms {
    // xy = framebuffer size (px), z = time (s), w = dpr (unused, 1)
    res_time: vec4f,
    // x=cell, y=speed, z=speedVar, w=density
    params: vec4f,
    // x=trail, y=glow, z=mutate, w=flicker
    params2: vec4f,
    // x=layers, y=glyphCount, z=atlasGrid, w=unused
    params3: vec4f,
    // body rgb + pad
    color: vec4f,
    // head rgb + pad
    head_color: vec4f,
}

@group(0) @binding(0) var<uniform> u: RainUniforms;
@group(0) @binding(1) var atlas_tex: texture_2d<f32>;
@group(0) @binding(2) var atlas_samp: sampler;

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
    // top-left origin UV
    out.uv = p[vi] * 0.5 + 0.5;
    out.uv.y = 1.0 - out.uv.y;
    return out;
}

fn hash11(p: f32) -> f32 {
    var n = fract(p * 0.1031);
    n *= n + 33.33;
    n *= n + n;
    return fract(n);
}

fn hash21(p: vec2f) -> f32 {
    var q = fract(vec3f(p.xyx) * 0.1031);
    q += dot(q, q.yzx + 33.33);
    return fract((q.x + q.y) * q.z);
}

// Canvas UI glyphMask UV (0.74 scale + 0.13 pad) — full glyph size, not dots.
// Soft cell-edge fade only so neighbors never share ink.
fn glyph_mask(px: vec2f, cell: f32, seed: f32) -> f32 {
    let id = floor(px / cell);
    var f = fract(px / cell);
    let edge = min(min(f.x, 1.0 - f.x), min(f.y, 1.0 - f.y));
    let edge_fade = smoothstep(0.0, 0.06, edge);
    f = f * 0.74 + 0.13;
    f.x = 1.0 - f.x;
    let tick = floor(u.res_time.z * u.params2.z * 1.6 + hash21(id + vec2f(seed)) * 9.0);
    let idx = floor(
        hash21(id * 1.71 + vec2f(seed + tick * 7.31, tick * 0.613)) * u.params3.y
    );
    let gx = idx % u.params3.z;
    let gy = floor(idx / u.params3.z);
    let auv = (vec2f(gx, gy) + f) / u.params3.z;
    return textureSampleLevel(atlas_tex, atlas_samp, auv, 0.0).a * edge_fade;
}

// Per-column speed: skewed slow — most crawl, few race.
// Floor raised so the slowest streams still move (was 0.22×).
fn col_speed(col: f32, seed: f32) -> f32 {
    let h = hash11(col * 0.37 + seed + 3.1);
    let h_slow = h * h;
    let variance = mix(0.38, 1.55, h_slow);
    return u.params.y * mix(1.0, variance, u.params.z);
}

fn col_offset(col: f32, seed: f32) -> f32 {
    return hash11(col * 1.713 + seed) * 9.0;
}

// Stable stratified columns — even coverage, no mid-fall pop on/off.
// Groups of 5 columns → exactly 2 always raining (fixed pick, no time epoch).
// Continuous phase T = t*sp+off so heads enter from the top and trails never
// hard-cut because a column "turned off".
fn column_active(col: f32, seed: f32) -> bool {
    let group = 5.0;
    let g = floor(col / group);
    let loc = col - g * group;
    let h0 = hash11(g * 19.7 + seed * 1.1);
    let h1 = hash11(g * 31.1 + seed * 2.3);
    var p0 = floor(h0 * group);
    var p1 = floor(h1 * group);
    if (abs(p1 - p0) < 0.5) {
        p1 = (p0 + 1.0) % group;
    }
    return abs(loc - p0) < 0.5 || abs(loc - p1) < 0.5;
}

@fragment
fn fs(in: VsOut) -> @location(0) vec4f {
    let res = max(u.res_time.xy, vec2f(1.0));
    let frag = in.uv * res;
    // yn: 1 at top of viewport, 0 at bottom
    let yn = 1.0 - frag.y / res.y;

    var rgb = vec3f(0.0);
    var orb = 0.0;
    // Single layer — multi-scale layers caused cross-column glyph overlap
    let cell = u.params.x;
    let trail = u.params2.x;
    let glow = u.params2.y;
    let flicker = u.params2.w;
    let t = u.res_time.z;
    let seed = 0.0;

    let col = floor(frag.x / cell);
    let col_live = column_active(col, seed);
    if (col_live) {
        let sp = col_speed(col, seed);
        let off = col_offset(col, seed);
        // Continuous phase: reveal band sweeps down; glyphs stay put
        let T = t * sp + off;
        let phase = fract(yn + T);
        let cyc = floor(yn + T);
        let cell_yn = max(cell / res.y, 1e-5);
        // How far behind the head, in whole glyph cells (0 = contact / leading edge)
        let cells_back = phase / cell_yn;

        // Canvas UI length: long inverse-phase trail (matches GlyphRainVanilla FRAG).
        //   b = trail / (phase * 22) − 0.04
        // This is droplet LENGTH — many cells of body jade, not tip color.
        let b = max(trail / (max(phase, 1e-5) * 22.0) - 0.04, 0.0);
        // Canvas UI head band: short bright boost at the reveal line only
        let head = 1.0 - smoothstep(0.0, cell_yn * 1.2, phase);

        if (b > 0.001 || head > 0.001) {
            let flick = 1.0 + flicker * 0.6 *
                sin(t * 14.0 + hash21(vec2f(col, cyc)) * 40.0 + phase * 30.0);
            let m = glyph_mask(frag, cell, seed + cyc * 0.173);

            // Brightness: long trail + short head glow (Canvas UI)
            let brightness = b * (1.0 + head * glow) * flick;

            // Tip COLOR only (not the circular orbs): hot primary at contact,
            // falls off within ~1 cell. Long trail stays pure body jade —
            // this is the "light on the end" / glow-board pen tip, separate
            // from trail length.
            let tip_col = exp(-cells_back * cells_back * 9.0);
            let col_rgb = mix(u.color.rgb, u.head_color.rgb, tip_col);

            rgb += col_rgb * m * brightness;
            // Extra hot spike only on the contact glyph (still not circular orb)
            rgb += u.head_color.rgb * m * tip_col * (1.4 + glow * 0.35);
        }
    }

    // Soft circular tip haze — unchanged (separate from glyph tip color)
    {
        let r = cell * 9.5;
        let span = i32(ceil(r / cell)) + 1;
        let col0 = i32(floor(frag.x / cell));
        for (var dc = -span; dc <= span; dc++) {
            let c = f32(col0 + dc);
            if (c < 0.0) { continue; }
            if (!column_active(c, seed)) { continue; }
            let sp = col_speed(c, seed);
            let off = col_offset(c, seed);
            let T = t * sp + off;
            let s = yn + T;
            let k0 = floor(s);
            for (var hi = 0; hi < 2; hi++) {
                let k = k0 + f32(hi);
                if (hash21(vec2f(c, k) * 1.91 + vec2f(7.3, seed)) > 0.385) {
                    continue;
                }
                let yn_head = k - T;
                if (yn_head < -0.08 || yn_head > 1.08) { continue; }
                let head_px = vec2f(
                    (c + 0.5) * cell,
                    (1.0 - yn_head) * res.y,
                );
                let d = length(frag - head_px);
                if (d >= r) { continue; }
                let t_fall = 1.0 - d / r;
                let att = pow(t_fall, 1.15);
                let lamp = 0.85 + 0.15 * hash11(c * 3.97 + k * 0.713);
                orb += att * lamp * 0.052;
            }
        }
    }

    orb = clamp(orb, 0.0, 0.18);
    let glow_col = mix(u.color.rgb, u.head_color.rgb, 0.35);
    rgb += glow_col * orb * 0.185;
    rgb = clamp(rgb, vec3f(0.0), vec3f(1.0));
    return vec4f(rgb, 1.0);
}
