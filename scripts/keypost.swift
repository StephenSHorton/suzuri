// Posts key events straight to one process (no focus change, so nothing
// lands in whatever app the person is using).
//   keypost <pid> text "<string>"   types a string
//   keypost <pid> key <keycode> [cmd|ctrl|alt|shift...]
//   keypost <pid> probe <n> <ms> <keycode>...  presses keys in a loop
import CoreGraphics
import Foundation

let args = CommandLine.arguments
let pid = pid_t(args[1])!
let src = CGEventSource(stateID: .hidSystemState)

// Modifiers go down as their own key events first (apps that poll key state,
// like ebiten, track modifiers from those), and every key is held for a
// moment like a real press.
let modKeys: [(CGEventFlags, CGKeyCode)] = [(.maskCommand, 55), (.maskShift, 56), (.maskAlternate, 58), (.maskControl, 59)]

func post(_ code: CGKeyCode, _ flags: CGEventFlags = [], text: String? = nil) {
    var held: CGEventFlags = []
    for (f, k) in modKeys where flags.contains(f) {
        held.insert(f)
        let e = CGEvent(keyboardEventSource: src, virtualKey: k, keyDown: true)!
        e.flags = held
        e.postToPid(pid)
        usleep(15000)
    }
    for down in [true, false] {
        let e = CGEvent(keyboardEventSource: src, virtualKey: code, keyDown: down)!
        e.flags = flags
        if let t = text {
            var u = Array(t.utf16)
            e.keyboardSetUnicodeString(stringLength: u.count, unicodeString: &u)
        }
        e.postToPid(pid)
        usleep(down ? 40000 : 15000)
    }
    for (f, k) in modKeys.reversed() where flags.contains(f) {
        held.remove(f)
        let e = CGEvent(keyboardEventSource: src, virtualKey: k, keyDown: false)!
        e.flags = held
        e.postToPid(pid)
        usleep(15000)
    }
}

func flags(_ names: ArraySlice<String>) -> CGEventFlags {
    var f: CGEventFlags = []
    for n in names {
        switch n {
        case "cmd": f.insert(.maskCommand)
        case "ctrl": f.insert(.maskControl)
        case "alt": f.insert(.maskAlternate)
        case "shift": f.insert(.maskShift)
        default: break
        }
    }
    return f
}

switch args[2] {
case "text":
    for ch in args[3] {
        if ch == "\n" { post(36); continue }
        post(0, text: String(ch))
    }
case "key":
    post(CGKeyCode(args[3])!, flags(args.dropFirst(4)))
case "probe":
    let n = Int(args[3])!, ms = UInt32(args[4])!
    let codes = args.dropFirst(5).map { CGKeyCode($0)! }
    for i in 0..<n {
        post(codes[i % codes.count])
        usleep(ms * 1000)
    }
default:
    print("usage: keypost <pid> text|key|probe ...")
}
