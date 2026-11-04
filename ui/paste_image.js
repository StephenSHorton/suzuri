// Clipboard images for paste (Go suzuri's clipboard_image_*.go): the paste
// event's image goes to Rust (paste_image_save), which writes it to a temp
// file and returns the path the pane gets.
const { invoke } = window.__TAURI__.core

// The first image on a paste event's clipboard, or null.
export function clipboardImage(data) {
  if (!data) return null
  for (const item of data.items || []) {
    if (item.kind === "file" && item.type.startsWith("image/")) return item.getAsFile()
  }
  return null
}

// Saves the image and resolves to its file path.
export async function saveClipboardImage(blob) {
  const bytes = new Uint8Array(await blob.arrayBuffer())
  return invoke("paste_image_save", bytes)
}
