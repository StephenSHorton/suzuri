import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
} from "react";
import { cn } from "@/lib/utils";
import "@xterm/xterm/css/xterm.css";

/**
 * Honest cell-grid terminal surface (xterm).
 * Shell/TUI content only — chrome is Kussetsu GPU, not drawn here.
 */
export type TerminalPaneHandle = {
  write: (data: string) => void;
  writeln: (data: string) => void;
  clear: () => void;
  focus: () => void;
  cols: () => number;
  rows: () => number;
};

type TerminalPaneProps = {
  className?: string;
  onResize?: (cols: number, rows: number) => void;
  onData?: (data: string) => void;
};

const inkstoneTheme = {
  // Slightly transparent so the glass frame rim still reads; cells stay legible.
  background: "#050a07f2",
  foreground: "#e8f5ee",
  cursor: "#00e676",
  cursorAccent: "#050a07",
  selectionBackground: "rgba(0, 230, 118, 0.28)",
  selectionForeground: "#e8f5ee",
  black: "#0a1410",
  red: "#ff8a80",
  green: "#00e676",
  yellow: "#ffb74d",
  blue: "#64b5f6",
  magenta: "#ce93d8",
  cyan: "#80deea",
  white: "#e8f5ee",
  brightBlack: "#6d8f7d",
  brightRed: "#ff8a80",
  brightGreen: "#3dff9a",
  brightYellow: "#ffcc80",
  brightBlue: "#90caf9",
  brightMagenta: "#e1bee7",
  brightCyan: "#b2ebf2",
  brightWhite: "#ffffff",
} as const;

export const TerminalPane = forwardRef<TerminalPaneHandle, TerminalPaneProps>(
  function TerminalPane({ className, onResize, onData }, ref) {
    const hostRef = useRef<HTMLDivElement>(null);
    const termRef = useRef<Terminal | null>(null);
    const fitRef = useRef<FitAddon | null>(null);
    const onResizeRef = useRef(onResize);
    const onDataRef = useRef(onData);
    onResizeRef.current = onResize;
    onDataRef.current = onData;

    useImperativeHandle(ref, () => ({
      write: (data) => termRef.current?.write(data),
      writeln: (data) => termRef.current?.writeln(data),
      clear: () => termRef.current?.clear(),
      focus: () => termRef.current?.focus(),
      cols: () => termRef.current?.cols ?? 0,
      rows: () => termRef.current?.rows ?? 0,
    }));

    useEffect(() => {
      const host = hostRef.current;
      if (!host) return;

      const term = new Terminal({
        convertEol: true,
        cursorBlink: true,
        cursorStyle: "bar",
        fontFamily:
          '"SF Mono", "JetBrains Mono", "Fira Code", ui-monospace, monospace',
        fontSize: 13,
        lineHeight: 1.25,
        theme: inkstoneTheme,
        allowProposedApi: true,
        scrollback: 5000,
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(host);
      fit.fit();
      term.focus();

      termRef.current = term;
      fitRef.current = fit;

      const dataDisp = term.onData((data) => {
        onDataRef.current?.(data);
      });

      const notifySize = () => {
        onResizeRef.current?.(term.cols, term.rows);
      };
      notifySize();

      const ro = new ResizeObserver(() => {
        try {
          fit.fit();
          notifySize();
        } catch {
          /* host unmounted mid-frame */
        }
      });
      ro.observe(host);

      return () => {
        dataDisp.dispose();
        ro.disconnect();
        term.dispose();
        termRef.current = null;
        fitRef.current = null;
      };
    }, []);

    return (
      <div className={cn("term-pane relative h-full min-h-0 w-full", className)}>
        <div
          ref={hostRef}
          className="absolute inset-0"
          data-terminal-pane
        />
      </div>
    );
  },
);
