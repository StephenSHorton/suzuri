/** True on macOS (incl. Tauri webview UA). */
export const isMac =
  typeof navigator !== "undefined" &&
  (/Mac|iPhone|iPod|iPad/i.test(navigator.platform) ||
    /Mac OS X|Macintosh/i.test(navigator.userAgent));
