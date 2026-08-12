import { getCurrentWindow } from "@tauri-apps/api/window";
import { View, Text, useViewport, rgba, type RGBA } from "kussetsu";
import { useEffect } from "react";
import { GpuGlyphRain } from "@/components/GpuGlyphRain";
import { isMac } from "@/lib/platform";
import {
  C,
  L,
  glassActive,
  glassPanel,
  terminalHole,
  type HoleRect,
} from "@/lib/theme";

export type ShellTab = {
  id: string;
  title: string;
  busy?: boolean;
};

type ShellChromeProps = {
  title: string;
  tabs: ShellTab[];
  activeId: string;
  draft: string;
  gridLabel: string;
  activeTitle: string;
  onHole: (hole: HoleRect) => void;
  onSelectTab: (id: string) => void;
  onCloseTab: (id: string) => void;
  onNewTab: () => void;
  onDraft: (v: string) => void;
  onSubmit: () => void;
  onOpenSettings: () => void;
};

async function withWin(fn: () => Promise<void>) {
  try {
    await fn();
  } catch {
    /* plain browser preview */
  }
}

/**
 * GPU-owned chrome: titlebar, tabs, settings, terminal frame, warp bar.
 * Terminal *cells* live in a DOM hole reported via onHole — not painted here.
 */
export function ShellChrome({
  title,
  tabs,
  activeId,
  draft,
  gridLabel,
  activeTitle,
  onHole,
  onSelectTab,
  onCloseTab,
  onNewTab,
  onDraft,
  onSubmit,
  onOpenSettings,
}: ShellChromeProps) {
  const { width: vw, height: vh } = useViewport();
  const hole = terminalHole(vw, vh);

  useEffect(() => {
    onHole(hole);
  }, [hole.x, hole.y, hole.w, hole.h, onHole]);

  const warpY = hole.y + hole.h + L.gap;

  return (
    <View style={{ width: vw, height: vh, direction: "column" }}>
      {/* Backdrop rain — first paint, non-glass, so glass refracts it */}
      <GpuGlyphRain cell={16} maxDrops={36} trail={9} />

      {/* Titlebar — traffic lights are a DOM overlay on mac (AppShell) for pixel centering */}
      <View
        style={{
          width: "stretch",
          height: L.titleH,
          shrink: 0,
          direction: "row",
          align: "center",
          justify: "space-between",
          // Leave room for traffic lights (DOM) so title doesn’t sit under them
          paddingLeft: isMac ? 78 : 12,
          paddingRight: 12,
          background: rgba("#08120d", 0.55),
        }}
      >
        <View style={{ direction: "row", align: "center", gap: 10 }}>
          <Text style={{ fontSize: 13, fontWeight: 600, color: C.mist100 }}>
            {title}
          </Text>
          <View
            glass={glassPanel}
            style={{
              paddingX: 8,
              paddingY: 3,
              radius: 999,
              background: C.chipIdle,
            }}
          >
            <Text
              style={{
                fontSize: 10,
                fontWeight: 700,
                letterSpacing: 1,
                color: C.jadeSoft,
              }}
            >
              KUSSETSU
            </Text>
          </View>
        </View>
        {!isMac ? (
          <View style={{ direction: "row", gap: 2 }}>
            <WinBtn
              label="Minimize"
              onActivate={() => withWin(() => getCurrentWindow().minimize())}
            >
              −
            </WinBtn>
            <WinBtn
              label="Maximize"
              onActivate={() =>
                withWin(() => getCurrentWindow().toggleMaximize())
              }
            >
              □
            </WinBtn>
            <WinBtn
              label="Close"
              danger
              onActivate={() => withWin(() => getCurrentWindow().close())}
            >
              ×
            </WinBtn>
          </View>
        ) : (
          <View style={{ width: 8, height: 1 }} />
        )}
      </View>

      {/* Tabs + logo + settings */}
      <View
        style={{
          width: "stretch",
          height: L.tabH,
          shrink: 0,
          direction: "row",
          align: "center",
          paddingX: L.pad,
          gap: 8,
        }}
      >
        {/* Suzuri mark — left of tabs */}
        <View
          glass={{ ...glassPanel, blur: 6, tint: 0.3 }}
          style={{
            width: 28,
            height: 28,
            radius: 8,
            shrink: 0,
            align: "center",
            justify: "center",
            background: C.chipIdle,
          }}
        >
          <Text style={{ fontSize: 13, fontWeight: 700, color: C.jade }}>
            硯
          </Text>
        </View>
        <View
          style={{
            grow: 1,
            direction: "row",
            align: "center",
            gap: 6,
            height: L.tabH,
          }}
        >
          {tabs.map((tab) => {
            const active = tab.id === activeId;
            return (
              <View
                key={tab.id}
                role="button"
                ariaLabel={`Tab ${tab.title}`}
                onActivate={() => onSelectTab(tab.id)}
                glass={active ? glassActive : glassPanel}
                style={{
                  height: 30,
                  paddingX: 12,
                  radius: L.chipRadius,
                  direction: "row",
                  align: "center",
                  gap: 8,
                  background: active ? C.chipActive : C.chipIdle,
                  border: active ? 1 : 0,
                  borderColor: active ? C.jadeDim : undefined,
                }}
              >
                <View
                  style={{
                    width: 6,
                    height: 6,
                    radius: 3,
                    background: tab.busy
                      ? C.amber
                      : active
                        ? C.jade
                        : C.mist500,
                  }}
                />
                <Text
                  style={{
                    fontSize: 12,
                    fontWeight: active ? 700 : 500,
                    color: active ? C.jadeSoft : C.mist300,
                  }}
                >
                  {tab.title}
                </Text>
                {tabs.length > 1 ? (
                  <View
                    role="button"
                    ariaLabel={`Close ${tab.title}`}
                    onActivate={() => onCloseTab(tab.id)}
                    style={{
                      width: 16,
                      height: 16,
                      radius: 4,
                      align: "center",
                      justify: "center",
                    }}
                  >
                    <Text style={{ fontSize: 11, color: C.mist500 }}>×</Text>
                  </View>
                ) : null}
              </View>
            );
          })}
          <View
            role="button"
            ariaLabel="New tab"
            onActivate={onNewTab}
            glass={glassPanel}
            style={{
              width: 28,
              height: 28,
              radius: L.chipRadius,
              align: "center",
              justify: "center",
              background: C.chipIdle,
            }}
          >
            <Text style={{ fontSize: 16, color: C.mist300 }}>+</Text>
          </View>
        </View>
        <View
          role="button"
          ariaLabel="Open settings"
          onActivate={onOpenSettings}
          glass={glassPanel}
          style={{
            height: 30,
            paddingX: 12,
            radius: L.chipRadius,
            direction: "row",
            align: "center",
            gap: 6,
            background: C.chipIdle,
          }}
        >
          <Text style={{ fontSize: 12, color: C.mist100 }}>Settings</Text>
        </View>
      </View>

      {/* Terminal glass frame — cells are the DOM hole on top */}
      <View
        glass={glassPanel}
        style={{
          absolute: { x: hole.x, y: hole.y },
          width: hole.w,
          height: hole.h,
          radius: L.radius,
          // Low alpha so refracted rain reads through the glass
          background: rgba("#030805", 0.28),
          direction: "column",
          padding: 10,
        }}
      >
        <View
          style={{
            width: "stretch",
            direction: "row",
            justify: "space-between",
            paddingX: 4,
            paddingBottom: 6,
          }}
        >
          <Text style={{ fontSize: 11, color: C.mist300 }}>{activeTitle}</Text>
          <Text style={{ fontSize: 11, color: C.mist500 }}>{gridLabel}</Text>
        </View>
        {/* Spacer where the DOM terminal sits */}
        <View style={{ grow: 1, width: "stretch" }} />
      </View>

      {/* Warp glass bar */}
      <View
        glass={glassPanel}
        style={{
          absolute: { x: L.pad, y: warpY },
          width: Math.max(80, vw - L.pad * 2),
          height: L.warpH,
          radius: L.radius,
          background: rgba("#040a07", 0.32),
          direction: "column",
          padding: 12,
          gap: 8,
        }}
      >
        <View style={{ direction: "row", align: "center", gap: 8 }}>
          <Text style={{ fontSize: 11, color: C.jadeSoft }}>
            ~/projects/suzuri
          </Text>
          <Text style={{ fontSize: 11, color: C.mist500 }}>
            · mock shell · PTY next
          </Text>
        </View>
        <View
          style={{
            direction: "row",
            align: "center",
            gap: 10,
            grow: 1,
            width: "stretch",
          }}
        >
          <Text style={{ fontSize: 14, color: C.jade }}>❯</Text>
          <View
            editable
            value={draft}
            onChange={onDraft}
            style={{ grow: 1, height: 28, justify: "center" }}
          >
            <Text
              style={{
                fontSize: 13.5,
                color: draft ? C.mist100 : C.mist500,
              }}
            >
              {draft || "Type a command…"}
            </Text>
          </View>
          <View
            role="button"
            ariaLabel="Submit command"
            onActivate={onSubmit}
            style={{
              width: 32,
              height: 32,
              radius: 10,
              align: "center",
              justify: "center",
              background: draft.trim()
                ? rgba("#00e676", 0.2)
                : rgba("#ffffff", 0.04),
            }}
          >
            <Text
              style={{
                fontSize: 14,
                fontWeight: 700,
                color: draft.trim() ? C.jade : C.mist500,
              }}
            >
              ↑
            </Text>
          </View>
        </View>
      </View>
    </View>
  );
}

function WinBtn({
  children,
  label,
  onActivate,
  danger,
}: {
  children: string;
  label: string;
  onActivate: () => void;
  danger?: boolean;
}) {
  return (
    <View
      role="button"
      ariaLabel={label}
      onActivate={onActivate}
      style={{
        width: 36,
        height: 28,
        radius: 8,
        align: "center",
        justify: "center",
      }}
    >
      <Text
        style={{
          fontSize: 14,
          color: (danger ? C.danger : C.mist400) as RGBA,
        }}
      >
        {children}
      </Text>
    </View>
  );
}

