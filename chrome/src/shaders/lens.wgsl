// Cursor glass lens drawn last, sampling the full scene RT.
// Uses @builtin(position) so the circle tracks the mouse in pixel space.

struct LensUniforms {
    // xy = logical size, zw = framebuffer size (physical px)
    size: vec4f,
    // xy = lens center LOGICAL px, z = radius LOGICAL, w = presence 0..1
    lens: vec4f,
    // ior, edge, bevel, depth
    glass: vec4f,
    // aberration, blur, reflection, shine
    glass2: vec4f,
}

@group(0) @binding(0) var<uniform> u: LensUniforms;
@group(0) @binding(1) var scene_tex: texture_2d<f32>;
@group(0) @binding(2) var scene_samp: sampler;

const PI: f32 = 3.14159265358979;
const AIR_IOR: f32 = 1.0003;

@vertex
fn vs(@builtin(vertex_index) vi: u32) -> @builtin(position) vec4f {
    var p = array<vec2f, 3>(
        vec2f(-1.0, -1.0),
        vec2f(3.0, -1.0),
        vec2f(-1.0, 3.0),
    );
    return vec4f(p[vi], 0.0, 1.0);
}

fn pow2(x: f32) -> f32 { return x * x; }
fn pow5(x: f32) -> f32 { let x2 = x * x; return x2 * x2 * x; }
fn linear_step(e0: f32, e1: f32, x: f32) -> f32 {
    return clamp((x - e0) / (e1 - e0), 0.0, 1.0);
}

fn ign(v: vec2f) -> f32 {
    return fract(52.9829189 * fract(0.06711056 * v.x + 0.00583715 * v.y));
}

// Sample scene: framebuffer / texture coords, origin top-left (WebGPU).
fn sample_scene_fb(fb_px: vec2f, lod: f32) -> vec3f {
    let fb = max(u.size.zw, vec2f(1.0));
    var uv = fb_px / fb;
    uv = clamp(uv, vec2f(0.001), vec2f(0.999));
    return textureSampleLevel(scene_tex, scene_samp, uv, lod).rgb;
}

fn ior_for_wavelength(base_ior: f32, aberration: f32, wavelength: f32) -> f32 {
    let ab = aberration * 0.1;
    return mix(
        base_ior + ab,
        base_ior - ab,
        1.0 - pow(1.0 - linear_step(450.0, 650.0, wavelength), 4.0),
    );
}

fn fresnel_schlick(cos_theta: f32, f0: f32) -> f32 {
    return f0 + (1.0 - f0) * pow5(1.0 - cos_theta);
}

@fragment
fn fs(@builtin(position) frag: vec4f) -> @location(0) vec4f {
    let fb = max(u.size.zw, vec2f(1.0));
    let logical = max(u.size.xy, vec2f(1.0));
    let fb_px = frag.xy;

    // Underlay: blit scene 1:1
    var col = sample_scene_fb(fb_px, 0.0);

    let presence = clamp(u.lens.w, 0.0, 1.0);
    let radius_log = u.lens.z;
    if (presence < 0.01 || radius_log < 1.0) {
        return vec4f(col, 1.0);
    }

    let scale = fb / logical;
    let center_fb = u.lens.xy * scale;
    let radius_fb = radius_log * min(scale.x, scale.y);

    let local = fb_px - center_fb;
    let dist = length(local);
    let sd = dist - radius_fb;

    let aa = 1.5;
    let mask = 1.0 - smoothstep(-aa, 0.0, sd);
    let alpha = mask * presence;
    if (alpha < 0.001) {
        return vec4f(col, 1.0);
    }

    let ior = max(u.glass.x, 1.01);
    let edge = u.glass.y;
    let bevel = max(u.glass.z, 0.5);
    let depth = min(u.glass.w, max(radius_log * 2.2, 40.0)) * min(scale.x, scale.y);
    let aberration = u.glass2.x;
    let blur = u.glass2.y;
    let reflection = u.glass2.z;
    // Canvas UI default shine is tiny (0.01); keep a gentle floor so arcs read
    let shine = max(u.glass2.w, 0.08);

    let edge_w = max(radius_fb * (1.0 - clamp(edge, 0.0, 0.98)), 1.0);
    let rim = pow(linear_step(-edge_w, 0.0, sd), bevel);

    let scatter = min(blur, 1.0) * 0.02;
    let rand_angle = ign(fb_px) * PI * 2.0;
    let flat_n = normalize(vec3f(
        sin(rand_angle) * scatter,
        cos(rand_angle) * scatter,
        1.0,
    ));
    let g2 = normalize(local + vec2f(1e-5));
    let rim_n = vec3f(g2, 0.0);
    let normal = normalize(mix(flat_n, rim_n, rim));

    let eta = AIR_IOR / ior;
    let incident = vec3f(0.0, 0.0, -1.0);
    var rv = refract(incident, normal, eta);
    if (dot(rv, rv) < 1e-8) {
        rv = vec3f(g2 * rim * 8.0, -1.0);
    }
    let z = max(abs(rv.z), 1e-4);
    let disp = rv.xy * (depth / z);

    var refracted: vec3f;
    if (aberration > 0.001) {
        let d0 = disp * (ior_for_wavelength(ior, aberration, 611.4) / ior);
        let d1 = disp * (ior_for_wavelength(ior, aberration, 549.1) / ior);
        let d2 = disp * (ior_for_wavelength(ior, aberration, 464.2) / ior);
        let r = sample_scene_fb(fb_px + d0, blur).r;
        let g = sample_scene_fb(fb_px + d1, blur).g;
        let b = sample_scene_fb(fb_px + d2, blur).b;
        refracted = vec3f(r, g, b);
    } else {
        refracted = sample_scene_fb(fb_px + disp, blur);
    }

    var glass = refracted;

    // Match panes: very quiet Fresnel (second pass)
    if (reflection > 0.001) {
        let n_dot_v = clamp(normal.z, 0.0, 1.0);
        let f0 = pow2((ior - AIR_IOR) / (ior + AIR_IOR));
        let fres = fresnel_schlick(n_dot_v, f0) * reflection;
        glass = mix(glass, glass * 0.94 + vec3f(0.4, 0.55, 0.5) * 0.06, clamp(fres * rim, 0.0, 0.1));
    }

    let ldot = dot(g2, normalize(vec2f(-0.6, 0.8)));
    let band = pow(rim, 1.8);
    let arcs = pow(abs(ldot), 3.0) * select(0.28, 0.5, ldot > 0.0);
    glass += band * (0.012 + arcs * 0.45) * shine;

    // Barely-there crystal edge
    let outline = (1.0 - smoothstep(0.0, 1.2, abs(sd))) * presence;
    glass += vec3f(0.28, 0.45, 0.38) * outline * 0.04;

    let shadow = smoothstep(radius_fb * 1.08, radius_fb * 0.72, dist) * 0.1 * presence;
    col *= 1.0 - shadow * (1.0 - mask);

    col = mix(col, glass, alpha);
    return vec4f(col, 1.0);
}
