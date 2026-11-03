// Inline images and Kitty graphics over a pane (card #288). The pane sends
// each image's bytes once ("img") and the list of images on screen whenever
// it changes ("imgpos"); this layer draws them in a layer above the text
// canvas, so the cell renderer stays as it is.
//
// Like Go suzuri: an image is drawn at its own size when it fits its box,
// else scaled down keeping its shape; Kitty images are centred in their
// cells (paintKittyPlacements), inline ones sit at the left of their block
// (the caption row under it is text). Clicking an inline image opens it
// large; Escape or a click closes it.

const PAD_X = 6 // the canvas's left and top padding (app.js)
const PAD_Y = 4

export class PaneImages {
  constructor(body) {
    this.layer = document.createElement("div")
    this.layer.className = "imgs"
    body.append(this.layer)
    this.images = new Map() // id -> {bitmap, name, path}
    this.waiting = new Map() // id -> Promise while decoding
    this.els = new Map() // placement key -> canvas
    this.last = []
  }

  async add(m) {
    const p = decode(m).then((bitmap) => {
      this.images.set(m.id, { bitmap, name: m.name, path: m.path })
      this.waiting.delete(m.id)
      this.draw()
    }).catch(() => this.waiting.delete(m.id))
    this.waiting.set(m.id, p)
  }

  drop(ids) {
    for (const id of ids) {
      this.images.get(id)?.bitmap.close?.()
      this.images.delete(id)
    }
    this.draw()
  }

  /// list: [image, row, col, cols, rows, z, inline, cx, cy, cw, ch]
  place(list) {
    this.last = list
    this.draw()
  }

  draw() {
    const { cw, ch } = this.metrics()
    const seen = new Set()
    const sorted = [...this.last].sort((a, b) => a[5] - b[5])
    for (const [id, row, col, cols, rows, z, inline, sx, sy, sw, sh] of sorted) {
      const im = this.images.get(id)
      if (!im) continue
      const key = `${id}`
      seen.add(key)
      let el = this.els.get(key)
      const crop = sw > 0 && sh > 0 ? [sx, sy, Math.min(sw, im.bitmap.width - sx), Math.min(sh, im.bitmap.height - sy)] : [0, 0, im.bitmap.width, im.bitmap.height]
      const boxW = cols * cw
      const boxH = rows * ch
      const [dw, dh] = fit(crop[2], crop[3], boxW, boxH)
      const x = PAD_X + col * cw + (inline ? 0 : (boxW - dw) / 2)
      const y = PAD_Y + row * ch + (inline ? 0 : (boxH - dh) / 2)
      const sig = `${dw}x${dh}:${crop}`
      if (!el) {
        el = document.createElement("canvas")
        el.className = inline ? "img inline" : "img"
        if (inline) el.addEventListener("click", () => openLarge(im))
        this.layer.append(el)
        this.els.set(key, el)
      }
      if (el.dataset.sig !== sig) {
        const dpr = window.devicePixelRatio || 1
        el.width = Math.max(1, Math.round(dw * dpr))
        el.height = Math.max(1, Math.round(dh * dpr))
        el.style.width = `${dw}px`
        el.style.height = `${dh}px`
        el.getContext("2d").drawImage(im.bitmap, crop[0], crop[1], crop[2], crop[3], 0, 0, el.width, el.height)
        el.dataset.sig = sig
      }
      el.style.transform = `translate(${x}px, ${y}px)`
      el.style.zIndex = String(z)
    }
    for (const [key, el] of this.els) {
      if (!seen.has(key)) {
        el.remove()
        this.els.delete(key)
      }
    }
  }

  metrics() {
    return this.cell || { cw: 9, ch: 18 }
  }

  setCell(cw, ch) {
    this.cell = { cw, ch }
    this.draw()
  }

  destroy() {
    for (const im of this.images.values()) im.bitmap.close?.()
    this.images.clear()
    this.layer.remove()
  }
}

// Go fitPreferNative: native size when it fits, else scaled down uniformly.
function fit(w, h, maxW, maxH) {
  if (w < 1 || h < 1 || maxW < 1 || maxH < 1) return [0, 0]
  if (w <= maxW && h <= maxH) return [w, h]
  const s = Math.min(maxW / w, maxH / h, 1)
  return [Math.max(1, Math.floor(w * s)), Math.max(1, Math.floor(h * s))]
}

function bytes(b64) {
  const s = atob(b64)
  const out = new Uint8Array(s.length)
  for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i)
  return out
}

async function inflate(data) {
  const ds = new DecompressionStream("deflate")
  const buf = await new Response(new Blob([data]).stream().pipeThrough(ds)).arrayBuffer()
  return new Uint8Array(buf)
}

// format 100: an image file the WebView decodes; 24/32: raw RGB/RGBA
// (Kitty), zlib when m.z.
async function decode(m) {
  let data = bytes(m.data)
  if (m.z) data = await inflate(data)
  if (m.format === 100) return createImageBitmap(new Blob([data]))
  const n = m.w * m.h
  if (!n) throw new Error("raw image without a size")
  const rgba = new Uint8ClampedArray(n * 4)
  if (m.format === 32) {
    rgba.set(data.subarray(0, n * 4))
  } else {
    for (let i = 0, j = 0; i < n; i++, j += 3) {
      rgba[i * 4] = data[j]
      rgba[i * 4 + 1] = data[j + 1]
      rgba[i * 4 + 2] = data[j + 2]
      rgba[i * 4 + 3] = 255
    }
  }
  return createImageBitmap(new ImageData(rgba, m.w, m.h))
}

// Go's image modal: the image as large as the window allows, its name under it.
let modal = null
function openLarge(im) {
  closeLarge()
  modal = document.createElement("div")
  modal.className = "img-modal"
  const c = document.createElement("canvas")
  const maxW = window.innerWidth * 0.9
  const maxH = window.innerHeight * 0.82
  const s = Math.min(maxW / im.bitmap.width, maxH / im.bitmap.height, 1)
  const dpr = window.devicePixelRatio || 1
  c.width = Math.round(im.bitmap.width * s * dpr)
  c.height = Math.round(im.bitmap.height * s * dpr)
  c.style.width = `${Math.round(im.bitmap.width * s)}px`
  c.style.height = `${Math.round(im.bitmap.height * s)}px`
  c.getContext("2d").drawImage(im.bitmap, 0, 0, c.width, c.height)
  const cap = document.createElement("div")
  cap.className = "cap"
  cap.textContent = im.path || im.name
  modal.append(c, cap)
  modal.addEventListener("click", closeLarge)
  document.body.append(modal)
}

export function closeLarge() {
  if (!modal) return false
  modal.remove()
  modal = null
  return true
}
