// Package assets holds files embedded into the suzuri binary.
package assets

import _ "embed"

// FontFaceBundled is the GDI face name after AddFontMemResourceEx.
// Bitmap-rooted GohuFont uni14 — designed for ~14px cells.
const FontFaceBundled = "GohuFont uni14 Nerd Font Mono"

// GohuFont uni14 Nerd Font Mono — see fonts/COPYING-LICENSE (WTFPL) and
// fonts/UPSTREAM-README.md (Nerd Fonts v3.4.0 archive notes).
// Registered at runtime via AddFontMemResourceEx (process-private).

//go:embed fonts/GohuFontuni14NerdFontMono-Regular.ttf
var BundledFontRegular []byte

// AppIconICO is the multi-size glowy 硯 application icon (PNG-in-ICO).
// PE builds also link cmd/suzuri/rsrc_windows_*.syso so Explorer shows it on the .exe.
// Runtime LoadImage falls back to these bytes when the PE resource is missing.
//
//go:embed icon/suzuri.ico
var AppIconICO []byte
