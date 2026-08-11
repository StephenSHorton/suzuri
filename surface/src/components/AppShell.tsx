import { getCurrentWindow } from "@tauri-apps/api/window";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { GpuCanvas, type GpuRoot } from "kussetsu";
import { useCallback, useEffect, useRef, useState } from "react";
import { SettingsChrome } from "@/components/SettingsChrome";
import { ShellChrome, type ShellTab } from "@/components/ShellChrome";
import {
  TerminalPane,
  type TerminalPaneHandle,
} from "@/components/TerminalPane";
import {
  buildGlassPanel,
  seedSurfaceGlassTuning,
} from "@/lib/glassDevPanel";
import {
  bannerLines,
  prompt,
  runMockCommand,
} from "@/lib/mockShell";
import { isMac } from "@/lib/platform";
import { L, type HoleRect } from "@/lib/theme";

async function withWin(fn: () => Promise<void>) {
  try {
    await fn();
  } catch {
    /* plain browser */
  }
}
/**
 * Root shell: Kussetsu owns chrome pixels; xterm sits in a measured DOM hole.
 */
export function AppShell() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const navigate = useNavigate();
  const onHome = pathname === "/" || pathname === "";

  const [hole, setHole] = useState<HoleRect | null>(null);
  const [tabs, setTabs] = useState<ShellTab[]>([
    { id: "1", title: "zsh" },
    { id: "2", title: "cargo", busy: true },
  ]);
  const [activeId, setActiveId] = useState("1");
  const [draft, setDraft] = useState("");
  const [seq, setSeq] = useState(100);
  const [grid, setGrid] = useState({ cols: 0, rows: 0 });
  const termRef = useRef<TerminalPaneHandle>(null);
  const booted = useRef(false);
  const rootRef = useRef<GpuRoot | null>(null);
  const glassPanelRef = useRef<ReturnType<typeof buildGlassPanel> | null>(null);

  const activeTitle =
    tabs.find((t) => t.id === activeId)?.title ?? "shell";
  const title = onHome ? "suzuri · surface" : "suzuri · settings";

  // Boot mock banner once the hole + xterm host exist (retry until ref is ready).
  useEffect(() => {
    if (!onHome || !hole || booted.current) return;
    let tries = 0;
    const id = window.setInterval(() => {
      const term = termRef.current;
      tries += 1;
      if (!term) {
        if (tries > 40) window.clearInterval(id);
        return;
      }
      window.clearInterval(id);
      if (booted.current) return;
      booted.current = true;
      for (const line of bannerLines()) term.writeln(line);
      term.write(prompt());
    }, 50);
    return () => window.clearInterval(id);
  }, [onHome, hole]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === ",") {
        e.preventDefault();
        void navigate({ to: "/settings" });
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [navigate]);

  const onHole = useCallback((h: HoleRect) => setHole(h), []);

  const onNewTab = () => {
    const id = String(seq);
    setSeq((n) => n + 1);
    setTabs((t) => [...t, { id, title: `shell ${id}` }]);
    setActiveId(id);
  };

  const onCloseTab = (id: string) => {
    setTabs((t) => {
      if (t.length <= 1) return t;
      const next = t.filter((x) => x.id !== id);
      if (activeId === id) setActiveId(next[0]?.id ?? "1");
      return next;
    });
  };

  const onSubmit = () => {
    const line = draft.trimEnd();
    if (!line) return;
    const term = termRef.current;
    if (!term) return;
    term.writeln(line);
    const out = runMockCommand(line);
    if (out[0] === "__CLEAR__") {
      term.clear();
      term.write(prompt());
      setDraft("");
      return;
    }
    for (const row of out) term.writeln(row);
    term.write(prompt());
    setDraft("");
  };

  const gridLabel =
    grid.cols > 0 ? `${grid.cols}×${grid.rows} · cell pane` : "… · cell pane";

  // Terminal content inset inside the glass frame header row
  const termInset = hole
    ? {
        left: hole.x + 10,
        top: hole.y + 28,
        width: Math.max(40, hole.w - 20),
        height: Math.max(40, hole.h - 38),
      }
    : null;

  const onGpuCreated = useCallback((root: GpuRoot) => {
    rootRef.current = root;
    seedSurfaceGlassTuning();
    // Dev glass tweaker (kussetsu demo pattern) — bottom-right
    glassPanelRef.current?.destroy();
    const panel = buildGlassPanel(() => root.requestRender());
    glassPanelRef.current = panel;
    document.querySelector(".app-root")?.appendChild(panel.el);
  }, []);

  useEffect(() => {
    return () => {
      glassPanelRef.current?.destroy();
      glassPanelRef.current = null;
      rootRef.current = null;
    };
  }, []);

  return (
    <div className={isMac ? "app-root app-root-mac" : "app-root"}>
      <GpuCanvas
        camera={false}
        // No full-screen backdrop shader — rain is GpuGlyphRain inside the scene
        // (DOM rain under an opaque WebGPU canvas was invisible).
        className="gpu-fill"
        style={{ width: "100%", height: "100%", background: "#050a07" }}
        onCreated={onGpuCreated}
        fallback={
          <div className="fallback">
            <p>
              <strong>WebGPU required</strong> for Kussetsu chrome.
            </p>
            <p>
              Use Chrome 113+, Edge, Safari 18+, or a recent Firefox — then
              reload.
            </p>
          </div>
        }
      >
        {onHome ? (
          <ShellChrome
            title={title}
            tabs={tabs}
            activeId={activeId}
            draft={draft}
            gridLabel={gridLabel}
            activeTitle={activeTitle}
            onHole={onHole}
            onSelectTab={setActiveId}
            onCloseTab={onCloseTab}
            onNewTab={onNewTab}
            onDraft={setDraft}
            onSubmit={onSubmit}
            onOpenSettings={() => void navigate({ to: "/settings" })}
          />
        ) : (
          <SettingsChrome onBack={() => void navigate({ to: "/" })} />
        )}
      </GpuCanvas>

      {/* macOS traffic lights — DOM so glyphs center with flex (GPU text baseline was off) */}
      {isMac && (
        <div className="traffic-lights" role="group" aria-label="Window">
          <button
            type="button"
            className="tl tl-close"
            aria-label="Close"
            onClick={() => void withWin(() => getCurrentWindow().close())}
          />
          <button
            type="button"
            className="tl tl-min"
            aria-label="Minimize"
            onClick={() => void withWin(() => getCurrentWindow().minimize())}
          />
          <button
            type="button"
            className="tl tl-zoom"
            aria-label="Zoom"
            onClick={() =>
              void withWin(() => getCurrentWindow().toggleMaximize())
            }
          />
        </div>
      )}

      {/* Tauri window drag — real DOM attribute (GPU can't host data-tauri-drag-region).
          macOS: inset left for traffic lights; elsewhere: inset right for win buttons. */}
      <div
        className={
          isMac ? "title-drag title-drag-mac" : "title-drag title-drag-win"
        }
        data-tauri-drag-region
        style={{ height: L.titleH }}
      />

      {/* Honest cell terminal — only shell output lives here */}
      {onHome && termInset && (
        <div
          className="term-hole"
          style={{
            left: termInset.left,
            top: termInset.top,
            width: termInset.width,
            height: termInset.height,
          }}
        >
          <TerminalPane
            ref={termRef}
            className="h-full w-full"
            onResize={(cols, rows) => setGrid({ cols, rows })}
          />
        </div>
      )}
    </div>
  );
}
