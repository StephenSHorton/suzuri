use glyphon::{Attrs, Buffer, Family, FontSystem, Metrics, Shaping, Weight};

fn main() {
    let data = include_bytes!("../../assets/fonts/GohuFontuni14NerdFontMono-Regular.ttf");
    let mut fs = FontSystem::new();
    fs.db_mut().load_font_data(data.to_vec());
    let mut gohu_id = None;
    let mut gohu_name = String::new();
    let mut gohu_w = Weight(500);
    for face in fs.db().faces() {
        for (name, _) in &face.families {
            if name.contains("Gohu") {
                gohu_id = Some(face.id);
                gohu_name = name.clone();
                gohu_w = face.weight;
            }
        }
    }
    let attrs = Attrs::new()
        .family(Family::Name(&gohu_name))
        .weight(gohu_w);
    let mut buf = Buffer::new(&mut fs, Metrics::new(14.0, 16.0));
    buf.set_size(&mut fs, Some(400.0), Some(20.0));
    buf.set_text(&mut fs, "Hi", attrs, Shaping::Advanced);
    buf.shape_until_scroll(&mut fs, false);
    for run in buf.layout_runs() {
        for g in run.glyphs.iter() {
            println!(
                "glyph font_id={:?} expected={:?} MATCH={}",
                g.font_id,
                gohu_id,
                Some(g.font_id) == gohu_id
            );
        }
    }
}
