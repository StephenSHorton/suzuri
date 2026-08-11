// Composite chrome over rain.
//
// Glass panels use the Canvas UI Glass *lens model* (GlassVanilla.ts defaults):
//   ior=1.5  edge=0.7  bevel=4  depth=250  aberration=1  blur=0  reflection=1  shine=0.01
// Adapted from a cursor lens to fixed rounded-rect UI panels sampling the rain RT.
// Source: https://github.com/DavidHDev/canvas-ui (MIT + Commons Clause) — ideas/params, not a paste of their stack.

struct FrameUniforms {
    // xy = logical size (css px), zw = framebuffer size
    size: vec4f,
    // x=time, y=dpr, z=panel_count, w=unused
    misc: vec4f,
    // Canvas UI glass defaults: x=ior, y=edge, z=bevel, w=depth (logical px)
    glass: vec4f,
    // x=aberration, y=blur, z=reflection, w=shine
    glass2: vec4f,
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
    out.uv = p[vi] * 0.5 + 0.5;
    out.uv.y = 1.0 - out.uv.y;
    return out;
}

fn pow2(x: f32) -> f32 { return x * x; }
fn pow5(x: f32) -> f32 { let x2 = x * x; return x2 * x2 * x; }

fn linear_step(e0: f32, e1: f32, x: f32) -> f32 {
    return clamp((x - e0) / (e1 - e0), 0.0, 1.0);
}

fn sd_round_box(p: vec2f, b: vec2f, r: f32) -> f32 {
    let q = abs(p) - b + vec2f(r);
    return length(max(q, vec2f(0.0))) + min(max(q.x, q.y), 0.0) - r;
}

// Sample rain RT. logical top-left origin → texture UV (no Y flip:
// rain is written with WebGPU top-left origin matching the scene).
fn sample_rain_raw(logical_px: vec2f, lod: f32) -> vec3f {
    let logical = max(u.size.xy, vec2f(1.0));
    var uv = logical_px / logical;
    uv = clamp(uv, vec2f(0.001), vec2f(0.999));
    return textureSampleLevel(rain_tex, rain_samp, uv, lod).rgb;
}

fn ign(v: vec2f) -> f32 {
    return fract(52.9829189 * fract(0.06711056 * v.x + 0.00583715 * v.y));
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

fn smith_schlick_denom(cos_theta: f32, k: f32) -> f32 {
    return cos_theta * (1.0 - k) + k;
}

fn ggx(roughness: f32, n_dot_l: f32, n_dot_v: f32, n_dot_h: f32) -> f32 {
    if (n_dot_l <= 0.0) { return 0.0; }
    let a2 = pow2(roughness);
    let d = a2 / (PI * pow2(pow2(n_dot_h) * (a2 - 1.0) + 1.0));
    let k = roughness * 0.5;
    let v = 1.0 / (smith_schlick_denom(n_dot_l, k)
        * smith_schlick_denom(clamp(n_dot_v, 0.0, 1.0), k));
    return n_dot_l * d * v;
}

/// Evaluate optical glass for one panel — same model as `lens.wgsl` (the good look).
/// Shape is rounded-rect SDF; optics match the cursor lens 1:1.
/// Returns (rgb, coverage) — coverage 0 outside.
fn eval_glass_panel(
    px: vec2f,
    center: vec2f,
    half: vec2f,
    radius: f32,
    kind: f32,
) -> vec4f {
    let ior = max(u.glass.x, 1.01);
    let edge = u.glass.y;
    let bevel = max(u.glass.z, 0.5);
    let min_half = min(half.x, half.y);
    let aberration = u.glass2.x;
    let blur = u.glass2.y;
    let reflection = u.glass2.z;
    // Match lens: gentle shine floor so rim arcs read
    let shine = max(u.glass2.w, 0.08);

    // --- Optical scale ---
    // Old path used min_half for depth/edge. Warp is short (~92px) so min_half ≈ 46
    // while Terminal min_half is hundreds → warp got ~½ the refraction depth and a
    // thinner bevel, so the two panes never matched. Primary panes (Terminal=0,
    // Warp=1) share a fixed optical scale (lens radius 120). Chips still soft-cap.
    let is_primary_pane = kind < 1.5; // Terminal | Warp
    let optical = select(
        min(min_half, 48.0), // chips / small chrome: size-capped
        120.0,               // terminal + warp: same as LENS_RADIUS
        is_primary_pane,
    );
    let depth = min(u.glass.w, max(optical * 2.2, 40.0));

    let local = px - center;
    let sd = sd_round_box(local, half, radius);

    let aa = 1.5;
    let mask = 1.0 - smoothstep(-aa, 0.0, sd);
    if (mask <= 0.001) {
        return vec4f(0.0);
    }

    let edge_w = max(optical * (1.0 - clamp(edge, 0.0, 0.98)), 1.0);
    let rim = pow(linear_step(-edge_w, 0.0, sd), bevel);

    // Lens-style normals: flat face z=+1, rim from SDF gradient (circle used radial g2)
    let scatter = min(blur, 1.0) * 0.02;
    let rand_angle = ign(px) * PI * 2.0;
    let flat_n = normalize(vec3f(
        sin(rand_angle) * scatter,
        cos(rand_angle) * scatter,
        1.0,
    ));
    let e = 1.0;
    let grad = vec2f(
        sd_round_box(local + vec2f(e, 0.0), half, radius)
            - sd_round_box(local - vec2f(e, 0.0), half, radius),
        sd_round_box(local + vec2f(0.0, e), half, radius)
            - sd_round_box(local - vec2f(0.0, e), half, radius),
    );
    let g2 = normalize(grad + vec2f(1e-5));
    let rim_n = vec3f(g2, 0.0);
    let normal = normalize(mix(flat_n, rim_n, rim));

    // Lens refraction: incident -Z, disp = rv.xy * (depth / |rv.z|)
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
        // Same 3-channel CA as lens (not the old 6-lobe panel path)
        let d0 = disp * (ior_for_wavelength(ior, aberration, 611.4) / ior);
        let d1 = disp * (ior_for_wavelength(ior, aberration, 549.1) / ior);
        let d2 = disp * (ior_for_wavelength(ior, aberration, 464.2) / ior);
        let r = sample_rain_raw(px + d0, blur).r;
        let g = sample_rain_raw(px + d1, blur).g;
        let b = sample_rain_raw(px + d2, blur).b;
        refracted = vec3f(r, g, b);
    } else {
        refracted = sample_rain_raw(px + disp, blur);
    }

    var glass = refracted;

    // Fresnel rim — very quiet (second pass down from chalky white)
    if (reflection > 0.001) {
        let n_dot_v = clamp(normal.z, 0.0, 1.0);
        let f0 = pow2((ior - AIR_IOR) / (ior + AIR_IOR));
        let fres = fresnel_schlick(n_dot_v, f0) * reflection;
        glass = mix(glass, glass * 0.94 + vec3f(0.4, 0.55, 0.5) * 0.06, clamp(fres * rim, 0.0, 0.1));
    }

    // Rim shine / arcs — whisper only
    let ldot = dot(g2, normalize(vec2f(-0.6, 0.8)));
    let band = pow(rim, 1.8);
    let arcs = pow(abs(ldot), 3.0) * select(0.28, 0.5, ldot > 0.0);
    glass += band * (0.012 + arcs * 0.45) * shine;

    // Crystal outline — barely-there edge
    let outline = 1.0 - smoothstep(0.0, 1.2, abs(sd));
    glass += vec3f(0.28, 0.45, 0.38) * outline * 0.04;

    return vec4f(glass, mask);
}

@fragment
fn fs(in: VsOut) -> @location(0) vec4f {
    let logical = u.size.xy;
    let px = in.uv * logical; // top-left origin

    var col = sample_rain_raw(px, 0.0);

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

        // Solid traffic-light kinds (6 close / 7 min / 8 zoom)
        if (p.kind > 5.5) {
            let d = sd_round_box(px - center, half, p.radius);
            let inside = 1.0 - smoothstep(-1.0, 1.0, d);
            if (inside <= 0.001) { continue; }
            var solid = vec3f(1.0, 0.373, 0.341); // #ff5f57 close
            if (p.kind > 6.5 && p.kind < 7.5) {
                solid = vec3f(0.996, 0.737, 0.180); // #febc2e min
            } else if (p.kind > 7.5) {
                solid = vec3f(0.157, 0.784, 0.251); // #28c840 zoom
            }
            let shade = 0.92 + 0.08 * smoothstep(p.radius, 0.0, length(px - center));
            col = mix(col, solid * shade, inside);
            continue;
        }

        let g = eval_glass_panel(px, center, half, p.radius, p.kind);
        if (g.a <= 0.001) { continue; }
        col = mix(col, g.rgb, g.a * 0.96);
    }

    return vec4f(col, 1.0);
}
