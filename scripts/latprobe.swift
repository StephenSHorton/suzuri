// latprobe: app-agnostic input latency on macOS. Posts key events straight to
// one process (CGEvent.postToPid, no focus change) and watches the app's
// window with ScreenCaptureKit; a sample is the key's post time to the
// display time of the first frame whose input-line pixels changed.
//
//   latprobe <pid> <n> <interval_ms> [out.json]
//
// It calibrates first: 2 s with no keys marks tiles that change on their
// own (spinners, other panes) as noise; then one "x" and its erase mark the
// tiles the echo touches. Only those tiles are compared afterwards. The
// probe alternates "x" and Delete, so the line never grows.
import AppKit
import CoreGraphics
import CoreMedia
import CoreVideo
import Foundation
import ScreenCaptureKit

let args = CommandLine.arguments
guard args.count >= 4, let pid = pid_t(args[1]), let n = Int(args[2]), let intervalMs = UInt32(args[3]) else {
    print("usage: latprobe <pid> <n> <interval_ms> [out.json]")
    exit(2)
}
let outPath = args.count > 4 ? args[4] : nil

var timebase = mach_timebase_info_data_t()
mach_timebase_info(&timebase)
func ms(_ dt: UInt64) -> Double { Double(dt) * Double(timebase.numer) / Double(timebase.denom) / 1e6 }

let keySrc = CGEventSource(stateID: .hidSystemState)
// Key down, held 40 ms like a person's press, then up. Apps that poll key
// state per tick (ebiten at 60 Hz) miss a press and release posted together.
func postKey(_ code: CGKeyCode) {
    CGEvent(keyboardEventSource: keySrc, virtualKey: code, keyDown: true)!.postToPid(pid)
    DispatchQueue.global().asyncAfter(deadline: .now() + .milliseconds(40)) {
        CGEvent(keyboardEventSource: keySrc, virtualKey: code, keyDown: false)!.postToPid(pid)
    }
}

let tile = 8 // pixels
let debug = ProcessInfo.processInfo.environment["LATPROBE_DEBUG"] != nil
final class Watch: NSObject, SCStreamOutput, @unchecked Sendable {
    let lock = NSCondition()
    var tilesW = 0, tilesH = 0
    var hashes: [UInt64] = []
    var frameTime: UInt64 = 0
    var frames = 0
    var noise: [Bool] = []
    var keyTiles: [Int] = []
    var calibrating = true
    var changedSinceMark: [Bool] = []

    func stream(_ stream: SCStream, didOutputSampleBuffer sb: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .screen,
              let atts = CMSampleBufferGetSampleAttachmentsArray(sb, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
              let a = atts.first,
              let raw = a[.status] as? Int, SCFrameStatus(rawValue: raw) == .complete,
              let pb = CMSampleBufferGetImageBuffer(sb) else { return }
        let disp = (a[.displayTime] as? UInt64) ?? mach_absolute_time()
        CVPixelBufferLockBaseAddress(pb, .readOnly)
        defer { CVPixelBufferUnlockBaseAddress(pb, .readOnly) }
        let w = CVPixelBufferGetWidth(pb), h = CVPixelBufferGetHeight(pb)
        let stride = CVPixelBufferGetBytesPerRow(pb)
        let base = CVPixelBufferGetBaseAddress(pb)!.assumingMemoryBound(to: UInt8.self)
        let tw = w / tile, th = h / tile
        var hs = [UInt64](repeating: 0xcbf29ce484222325, count: tw * th)
        let only: [Int]? = calibrating ? nil : keyTiles
        func hashTile(_ i: Int) {
            let tx = i % tw, ty = i / tw
            var hv: UInt64 = 0xcbf29ce484222325
            for y in (ty * tile)..<(ty * tile + tile) {
                let row = base + y * stride + tx * tile * 4
                for x in 0..<(tile * 4) {
                    hv = (hv ^ UInt64(row[x])) &* 0x100000001b3
                }
            }
            hs[i] = hv
        }
        if let only { for i in only { hashTile(i) } } else { for i in 0..<(tw * th) { hashTile(i) } }
        lock.lock()
        if tw != tilesW || th != tilesH {
            tilesW = tw; tilesH = th
            noise = [Bool](repeating: false, count: tw * th)
            changedSinceMark = noise
        } else if calibrating {
            var c = 0
            for i in 0..<(tw * th) where hs[i] != hashes[i] { changedSinceMark[i] = true; c += 1 }
            _ = c
        }
        hashes = hs
        frameTime = disp
        frames += 1
        lock.broadcast()
        lock.unlock()
    }

    func keyHash() -> [UInt64] { keyTiles.map { hashes[$0] } }
}

func run() async throws {
    let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
    // The app's largest normal window (suzuri also owns small helper windows).
    guard let win = content.windows
        .filter({ $0.owningApplication?.processID == pid && $0.windowLayer == 0 && $0.isOnScreen })
        .max(by: { $0.frame.width * $0.frame.height < $1.frame.width * $1.frame.height }) else {
        print("no window for pid \(pid)"); exit(1)
    }
    // Capture the window's area of the display (what is really on screen);
    // window-only capture misses some apps' Metal layers.
    guard let display = content.displays.first(where: { $0.frame.intersects(win.frame) }) else {
        print("no display for the window"); exit(1)
    }
    let filter = SCContentFilter(display: display, including: [win])
    let cfg = SCStreamConfiguration()
    cfg.sourceRect = CGRect(x: win.frame.minX - display.frame.minX, y: win.frame.minY - display.frame.minY, width: win.frame.width, height: win.frame.height)
    let scale = 2.0
    cfg.width = Int(win.frame.width * scale)
    cfg.height = Int(win.frame.height * scale)
    cfg.minimumFrameInterval = CMTime(value: 1, timescale: 240)
    cfg.pixelFormat = kCVPixelFormatType_32BGRA
    cfg.showsCursor = false
    cfg.queueDepth = 4
    let watch = Watch()
    let stream = SCStream(filter: filter, configuration: cfg, delegate: nil)
    try stream.addStreamOutput(watch, type: .screen, sampleHandlerQueue: DispatchQueue(label: "frames", qos: .userInteractive))
    try await stream.startCapture()

    // Calibrate: noise first, then the tiles one echo changes.
    try await Task.sleep(nanoseconds: 4_000_000_000)
    watch.lock.lock()
    watch.noise = watch.changedSinceMark
    watch.changedSinceMark = [Bool](repeating: false, count: watch.noise.count)
    watch.lock.unlock()
    // Six presses; a tile counts only if every press changed it, so output
    // in other panes during calibration doesn't pass for the echo.
    var keySet: Set<Int>? = nil
    for attempt in 0..<3 {
    keySet = nil
    for k in 0..<6 {
        watch.lock.lock()
        watch.changedSinceMark = [Bool](repeating: false, count: watch.noise.count)
        watch.lock.unlock()
        postKey(k % 2 == 0 ? 7 : 51)
        try await Task.sleep(nanoseconds: 300_000_000)
        watch.lock.lock()
        let changed = Set((0..<watch.noise.count).filter { watch.changedSinceMark[$0] && !watch.noise[$0] })
        watch.lock.unlock()
        if debug {
            let raw = (0..<watch.noise.count).filter { watch.changedSinceMark[$0] }.count
            FileHandle.standardError.write("press \(k): changed \(raw), not noisy \(changed.count)\n".data(using: .utf8)!)
        }
        keySet = keySet.map { $0.intersection(changed) } ?? changed
    }
    if !(keySet ?? []).isEmpty { break }
    FileHandle.standardError.write("calibration attempt \(attempt) found nothing; again\n".data(using: .utf8)!)
    }
    watch.lock.lock()
    watch.keyTiles = Array(keySet ?? []).sorted()
    watch.calibrating = false
    let noisy = watch.noise.filter { $0 }.count
    watch.lock.unlock()
    FileHandle.standardError.write("window \(Int(win.frame.width))x\(Int(win.frame.height)), frames \(watch.frames)\n".data(using: .utf8)!)
    FileHandle.standardError.write("calibrated: \(watch.keyTiles.count) key tiles, \(noisy) noisy tiles of \(watch.noise.count)\n".data(using: .utf8)!)
    if watch.keyTiles.isEmpty { print("calibration found no echo tiles"); exit(1) }
    try await Task.sleep(nanoseconds: 300_000_000)

    var samples: [Double] = []
    var timeouts = 0
    for i in 0..<n {
        watch.lock.lock()
        let before = watch.keyHash()
        watch.lock.unlock()
        let t0 = mach_absolute_time()
        postKey(i % 2 == 0 ? 7 : 51)
        var got: Double? = nil
        let deadline = Date().addingTimeInterval(0.5)
        watch.lock.lock()
        while got == nil {
            if watch.frameTime > t0 && watch.keyHash() != before {
                got = ms(watch.frameTime - t0)
                break
            }
            if !watch.lock.wait(until: deadline) { break }
        }
        watch.lock.unlock()
        if let g = got { samples.append(g) } else { timeouts += 1 }
        usleep(intervalMs * 1000)
    }
    try await stream.stopCapture()

    let s = samples.sorted()
    func p(_ q: Double) -> Double { s.isEmpty ? 0 : s[min(s.count - 1, Int((q / 100 * Double(s.count)).rounded(.up)) - 1)] }
    let report: [String: Any] = [
        "n": s.count, "timeouts": timeouts,
        "p50": p(50), "p90": p(90), "p99": p(99), "max": s.last ?? 0,
        "mean": s.isEmpty ? 0 : s.reduce(0, +) / Double(s.count),
        "key_tiles": watch.keyTiles.count,
    ]
    let json = try JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys])
    print(String(data: json, encoding: .utf8)!)
    if let outPath { try json.write(to: URL(fileURLWithPath: outPath)) }
    exit(0)
}

_ = NSApplication.shared
Task { do { try await run() } catch { print("error: \(error)"); exit(1) } }
dispatchMain()
