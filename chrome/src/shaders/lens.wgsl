// Full-screen cursor glass lens (Canvas UI style), drawn *after* UI text.
// Samples the composited scene so refraction includes rain, glass panels, and glyphs.

struct LensUniforms {
    // xy = logical size, zw = framebuffer size
    size: vec4f,
    // xy = lens center (logical), z = radius, w = presence 0..1
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
const INCIDENT: vec3f = vec3f(0.0, 0.0, 1.0);

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
    // uv in 0..1, y down (match logical top-left)
    out.uv = vec2f(p[vi].x * 0.5 + 0.5, 0.5 - p[vi].y * 0.5);
    return out;
}

fn pow2(x: f32) -> f32 { return x * x; }
fn pow5(x: f32) -> f32 { let x2 = x * x; return x2 * x2 * x; }
fn linear_step(e0: f32, e1: f32, x: f32) -> f32 {
    return clamp((x - e0) / (e1 - e0), 0.0, 1.0);
}

fn sd_circle(p: vec2f, r: f32) -> f32 {
    return length(p) - r;
}

fn ign(v: vec2f) -> f32 {
    return fract(52.9829189 * fract(0.06711056 * v.x + 0.00583715 * v.y));
}

// logical top-left px → texture UV (y-down framebuffer matches our composite output)
fn sample_scene(logical_px: vec2f, lod: f32) -> vec3f {
    let logical = u.size.xy;
    var uv = logical_px / logical;
    uv = clamp(uv, vec2f(0.001), vec2f(0.999));
    // composite writes with top-left logical → NDC flip; resulting texture is y-down in memory
    // Our composite fs uses in.uv with y flipped for top-left; the RT is standard GL-style
    // after present. We rendered to scene_tex the same way as swapchain, so UV y is up in
    // wgpu render targets (origin bottom-left). Convert top-left logical → bottom-left UV:
    let suv = vec2f(uv.x, 1.0 - uv.y);
    return textureSampleLevel(scene_tex, scene_samp, suv, lod).rgb;
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

fn sample_refraction(
    base_px: vec2f,
    rim: f32,
    normal: vec3f,
    glass_ior: f32,
    depth: f32,
    blur: f32,
) -> vec3f {
    var rv = refract(INCIDENT, normal, AIR_IOR / glass_ior);
    let z = abs(rv.z);
    if (z < 1e-4) {
        return sample_scene(base_px, blur);
    }
    rv = rv / (z / max(depth, 1.0));
    return sample_scene(base_px + rv.xy, blur * (1.0 + rim));
}

@fragment
fn fs(in: VsOut) -> @location(0) vec4f {
    let logical = u.size.xy;
    let px = in.uv * logical;

    // Always start from the scene under the cursor
    var col = sample_scene(px, 0.0);

    let presence = u.lens.w;
    let radius = u.lens.z;
    if (presence < 0.004 || radius < 1.0) {
        return vec4f(col, 1.0);
    }

    let center = u.lens.xy;
    let local = px - center;
    let sd = sd_circle(local, radius);

    let aa = 1.5;
    let mask = 1.0 - smoothstep(-aa, 0.0, sd);
    let alpha = mask * min(presence * 5.0, 1.0);
    if (alpha < 0.001) {
        return vec4f(col, 1.0);
    }

    let ior = u.glass.x;
    let edge = u.glass.y;
    let bevel = max(u.glass.z, 0.5);
    let depth = min(u.glass.w, max(radius * 2.2, 40.0));
    let aberration = u.glass2.x;
    let blur = u.glass2.y;
    let reflection = u.glass2.z;
    let shine = u.glass2.w;

    let edge_w = max(radius * (1.0 - clamp(edge, 0.0, 0.98)), 1.0);
    let rim = pow(linear_step(-edge_w, 0.0, sd), bevel);

    let scatter = min(blur, 1.0) * 0.02;
    let rand_angle = ign(px) * PI * 2.0;
    // Face camera (+Z out of screen toward viewer in our refract setup)
    let flat_n = normalize(vec3f(
        sin(rand_angle) * scatter,
        cos(rand_angle) * scatter,
        1.0,
    ));
    // Circle gradient normal
    let g = normalize(local + vec2f(1e-5));
    let rim_n = vec3f(g, 0.0);
    let normal = normalize(mix(flat_n, rim_n, rim));

    var refracted: vec3f;
    if (aberration > 0.001) {
        refracted = sample_refraction(px, rim, normal, ior_for_wavelength(ior, aberration, 611.4), depth, blur)
            * vec3f(1.0, 0.0, 0.0);
        refracted += sample_refraction(px, rim, normal, ior_for_wavelength(ior, aberration, 570.5), depth, blur)
            * vec3f(1.0, 1.0, 0.0);
        refracted += sample_refraction(px, rim, normal, ior_for_wavelength(ior, aberration, 549.1), depth, blur)
            * vec3f(0.0, 1.0, 0.0);
        refracted += sample_refraction(px, rim, normal, ior_for_wavelength(ior, aberration, 491.4), depth, blur)
            * vec3f(0.0, 1.0, 1.0);
        refracted += sample_refraction(px, rim, normal, ior_for_wavelength(ior, aberration, 464.2), depth, blur)
            * vec3f(0.0, 0.0, 1.0);
        refracted += sample_refraction(px, rim, normal, ior_for_wavelength(ior, aberration, 374.0), depth, blur)
            * vec3f(1.0, 0.0, 1.0);
        refracted = refracted / 3.0;
    } else {
        refracted = sample_refraction(px, rim, normal, ior, depth, blur);
    }

    var glass = refracted;

    if (reflection > 0.001) {
        let V = vec3f(0.0, 0.0, 1.0);
        let n_dot_v = clamp(dot(V, normal), 0.0, 1.0);
        let f0 = pow2((ior - AIR_IOR) / (ior + AIR_IOR));
        let fresnel_v = fresnel_schlick(n_dot_v, f0) * reflection;
        // Soft white rim reflection (no env map)
        let rim_glint = pow(rim, 1.6) * fresnel_v;
        glass = mix(glass, glass * 0.85 + vec3f(0.9, 0.95, 1.0) * 0.35, clamp(rim_glint, 0.0, 0.6));
    }

    if (shine > 0.001) {
        let ldot = dot(normalize(local + vec2f(1e-5)), normalize(vec2f(-0.6, 0.8)));
        let band = pow(rim, 1.8);
        let arcs = pow(abs(ldot), 3.0) * select(0.28, 0.5, ldot > 0.0);
        glass += band * (0.04 + arcs) * max(shine, 0.35);
    }

    // Bright rim outline so the lens is always obvious while dialing
    let outline = smoothstep(2.5, 0.0, abs(sd)) * presence;
    glass += vec3f(0.85, 1.0, 0.95) * outline * 0.45;

    col = mix(col, glass, alpha);
    return vec4f(col, 1.0);
}
