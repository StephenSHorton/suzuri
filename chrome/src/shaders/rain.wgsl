// Full-screen matrix-style glyph rain (procedural cells).
// Real atlas glyphs can land later; motion + columns match the product look.

struct RainUniforms {
    // resolution.xy = framebuffer px, z = time seconds, w = unused
    res_time: vec4f,
}

@group(0) @binding(0) var<uniform> u: RainUniforms;

struct VsOut {
    @builtin(position) pos: vec4f,
    @location(0) uv: vec2f,
}

@vertex
fn vs(@builtin(vertex_index) vi: u32) -> VsOut {
    // Fullscreen triangle
    var p = array<vec2f, 3>(
        vec2f(-1.0, -1.0),
        vec2f(3.0, -1.0),
        vec2f(-1.0, 3.0),
    );
    var out: VsOut;
    out.pos = vec4f(p[vi], 0.0, 1.0);
    out.uv = p[vi] * 0.5 + 0.5;
    out.uv.y = 1.0 - out.uv.y; // top-left origin feel
    return out;
}

fn hash11(p: f32) -> f32 {
    var n = fract(p * 0.1031);
    n *= n + 33.33;
    n *= n + n;
    return fract(n);
}

fn hash21(p: vec2f) -> f32 {
    var p3 = fract(vec3f(p.xyx) * 0.1031);
    p3 += dot(p3, p3.yzx + 33.33);
    return fract((p3.x + p3.y) * p3.z);
}

// Multi-stroke “glyph” bits — denser matrix look without a font atlas.
fn glyph_mask(f: vec2f, seed: f32) -> f32 {
    let g = f * 2.0 - 1.0;
    let s = hash11(seed * 17.1);
    let s2 = hash11(seed * 91.7);
    let s3 = hash11(seed * 3.1);
    // primary vertical
    var m = smoothstep(0.32, 0.12, abs(g.x - (s * 0.35 - 0.18)));
    // secondary vertical
    m = max(m, smoothstep(0.38, 0.14, abs(g.x + 0.25 + s2 * 0.2)) * step(0.4, s2));
    // crossbars
    m = max(m, smoothstep(0.42, 0.18, abs(g.y - 0.2)) * step(0.5, s) * smoothstep(0.55, 0.2, abs(g.x)));
    m = max(m, smoothstep(0.42, 0.18, abs(g.y + 0.35)) * step(0.6, s3) * smoothstep(0.5, 0.15, abs(g.x + 0.1)));
    // dots / ticks
    let d1 = length(g - vec2f(0.15, -0.55 + s * 0.1));
    m = max(m, smoothstep(0.18, 0.06, d1) * step(0.55, s2));
    m *= step(0.06, f.x) * step(f.x, 0.94) * step(0.05, f.y) * step(f.y, 0.95);
    return clamp(m, 0.0, 1.0);
}

@fragment
fn fs(in: VsOut) -> @location(0) vec4f {
    let res = u.res_time.xy;
    let t = u.res_time.z;
    let px = in.uv * res;

    let cell = 15.0;
    let col = floor(px.x / cell);
    let seed = hash11(col * 12.9898 + 78.233);
    let speed = 0.14 + seed * 0.42;
    let phase = seed * 6.28318;
    // column head position in 0..1 of screen height, looping
    let head = fract(in.uv.y * 0.35 - t * speed + phase);

    let id = floor(px / cell);
    let f = fract(px / cell);
    // scramble glyph over time
    let tick = floor(t * 1.8 + hash21(id) * 9.0);
    let gmask = glyph_mask(f, hash21(id + tick));

    // trail fades behind the head (smaller head.y-local → brighter)
    let trail = clamp(0.75 / (head * 18.0 + 0.15) - 0.03, 0.0, 1.2);
    let head_glow = 1.0 - smoothstep(0.0, 0.06, head);
    let body = gmask * trail;
    let g = body * (0.55 + 0.45 * seed) + gmask * head_glow * 1.1;

    let jade = vec3f(0.0, 0.9, 0.46) * g;
    let base = vec3f(0.02, 0.04, 0.028);
    let head_col = vec3f(0.86, 1.0, 0.92) * head_glow * gmask;

    return vec4f(base + jade * 0.9 + head_col * 0.85, 1.0);
}
