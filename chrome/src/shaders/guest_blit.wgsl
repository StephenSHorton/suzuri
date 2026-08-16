// Guest framebuffer blit into the mosaic well (logical px, top-left origin).

struct Uni {
    size: vec4f,  // xy = logical window
    well: vec4f,  // xywh dest
    glass: vec4f, // xywh clip
    clip: vec4f,  // x = corner radius
}

@group(0) @binding(0) var<uniform> u: Uni;
@group(0) @binding(1) var tex: texture_2d<f32>;
@group(0) @binding(2) var samp: sampler;

struct VsOut {
    @builtin(position) pos: vec4f,
    @location(0) uv: vec2f,
}

fn sd_round_box(p: vec2f, half: vec2f, r: f32) -> f32 {
    let q = abs(p) - half + vec2f(r);
    return min(max(q.x, q.y), 0.0) + length(max(q, vec2f(0.0))) - r;
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
    let px = in.uv * u.size.xy;
    let d = px - u.well.xy;
    if (d.x < 0.0 || d.y < 0.0 || d.x >= u.well.z || d.y >= u.well.w) {
        discard;
    }
    let g = u.glass;
    if (g.z > 1.0 && g.w > 1.0) {
        let center = g.xy + g.zw * 0.5;
        let half = g.zw * 0.5;
        if (sd_round_box(px - center, half, max(u.clip.x, 0.0)) > 0.0) {
            discard;
        }
    }
    let uv = vec2f(d.x / max(u.well.z, 1.0), d.y / max(u.well.w, 1.0));
    return textureSampleLevel(tex, samp, uv, 0.0);
}
