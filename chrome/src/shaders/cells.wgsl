// Draw a cell-grid texture into the terminal hole (logical px → clip).

struct CellUniforms {
    // xy = logical size, zw = framebuffer size
    size: vec4f,
    // terminal hole x,y,w,h logical
    hole: vec4f,
}

@group(0) @binding(0) var<uniform> u: CellUniforms;
@group(0) @binding(1) var cell_tex: texture_2d<f32>;
@group(0) @binding(2) var cell_samp: sampler;

struct VsOut {
    @builtin(position) pos: vec4f,
    @location(0) uv: vec2f,
}

@vertex
fn vs(@builtin(vertex_index) vi: u32) -> VsOut {
    // Unit quad 0..1
    var corners = array<vec2f, 6>(
        vec2f(0.0, 0.0), vec2f(1.0, 0.0), vec2f(0.0, 1.0),
        vec2f(0.0, 1.0), vec2f(1.0, 0.0), vec2f(1.0, 1.0),
    );
    let c = corners[vi];
    let hole = u.hole;
    let logical = u.size.xy;
    // top-left origin logical → NDC
    let px = hole.xy + c * hole.zw;
    let ndc = vec2f(
        (px.x / logical.x) * 2.0 - 1.0,
        1.0 - (px.y / logical.y) * 2.0,
    );
    var out: VsOut;
    out.pos = vec4f(ndc, 0.0, 1.0);
    out.uv = c;
    return out;
}

@fragment
fn fs(in: VsOut) -> @location(0) vec4f {
    let col = textureSample(cell_tex, cell_samp, in.uv);
    return col;
}
