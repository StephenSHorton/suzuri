// Linear downsample of a 2× text overlay onto the scene RT.
// Glyphon's atlas sampler is nearest, so sub-retina Gohu (14px) looks 1-bit.
// Rasterize at 2× then blit with a linear sampler for Mac-like coverage AA.

@group(0) @binding(0) var tex: texture_2d<f32>;
@group(0) @binding(1) var samp: sampler;

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

@fragment
fn fs(in: VsOut) -> @location(0) vec4f {
    return textureSampleLevel(tex, samp, in.uv, 0.0);
}
