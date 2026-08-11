/**
 * Tiny mock shell for the vertical slice until a real PTY is wired.
 * Speaks plain text + a few ANSI bits into the cell grid (xterm).
 */

const PROMPT = "\x1b[32m❯\x1b[0m ";
const DIM = "\x1b[90m";
const JADE = "\x1b[92m";
const ERR = "\x1b[91m";
const RESET = "\x1b[0m";

export function bannerLines(): string[] {
  return [
    `${JADE}suzuri surface${RESET}  ·  Kussetsu chrome + cell-grid terminal`,
    `${DIM}rule: anything that isn't shell output never snaps to a character cell${RESET}`,
    "",
    `${DIM}mock PTY — try: help, clear, uname, whoami, theme, rain${RESET}`,
    "",
  ];
}

export function prompt(): string {
  return `${DIM}stephen@inkstone ~/projects/suzuri${RESET}\r\n${PROMPT}`;
}

export function runMockCommand(line: string): string[] {
  const trimmed = line.trim();
  if (!trimmed) return [];

  if (trimmed === "clear") {
    return ["__CLEAR__"];
  }

  if (trimmed === "help") {
    return [
      "mock commands: help, clear, uname, whoami, theme, rain",
      `${DIM}(real ConPTY / POSIX PTY comes later)${RESET}`,
    ];
  }

  if (trimmed.startsWith("uname")) {
    return ["Darwin arm64"];
  }

  if (trimmed === "whoami") {
    return ["surface-slice"];
  }

  if (trimmed === "theme") {
    return [
      `${JADE}inkstone · Kussetsu GPU chrome · cell pane only for shell${RESET}`,
    ];
  }

  if (trimmed === "rain") {
    return [
      `${DIM}glyph rain is real half-width katakana + digits (Canvas 2D), Canvas UI–style.${RESET}`,
      `${DIM}chrome is Kussetsu WebGPU glass; cells stay in the xterm hole.${RESET}`,
    ];
  }

  return [
    `${ERR}mock: command not found: ${trimmed.split(/\s+/)[0]}${RESET}`,
    `${DIM}(PTY not connected — UI architecture slice)${RESET}`,
  ];
}

export { PROMPT, DIM, JADE, ERR, RESET };
