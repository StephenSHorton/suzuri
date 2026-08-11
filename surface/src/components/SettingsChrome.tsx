import { View, Text, useViewport, rgba } from "kussetsu";
import { GpuGlyphRain } from "@/components/GpuGlyphRain";
import { C, L, glassPanel } from "@/lib/theme";

type SettingsChromeProps = {
  onBack: () => void;
};

export function SettingsChrome({ onBack }: SettingsChromeProps) {
  const { width: vw, height: vh } = useViewport();

  return (
    <View
      style={{
        width: vw,
        height: vh,
        direction: "column",
        padding: L.pad,
        gap: 12,
      }}
    >
      <GpuGlyphRain cell={16} maxDrops={28} trail={8} />
      <View style={{ direction: "row", align: "center", gap: 12, height: 40 }}>
        <View
          role="button"
          ariaLabel="Back to terminal"
          onActivate={onBack}
          glass={glassPanel}
          style={{
            height: 32,
            paddingX: 12,
            radius: 10,
            direction: "row",
            align: "center",
            gap: 6,
            background: C.chipIdle,
          }}
        >
          <Text style={{ fontSize: 13, color: C.mist300 }}>← Back</Text>
        </View>
        <Text style={{ fontSize: 18, fontWeight: 700, color: C.mist100 }}>
          Settings
        </Text>
      </View>

      <View
        glass={glassPanel}
        style={{
          width: "stretch",
          padding: 16,
          radius: L.radius,
          background: rgba("#040a07", 0.55),
          direction: "column",
          gap: 8,
        }}
      >
        <Text style={{ fontSize: 14, fontWeight: 700, color: C.jadeSoft }}>
          Renderer
        </Text>
        <Text style={{ fontSize: 13, color: C.mist300 }}>
          Chrome is Kussetsu (WebGPU) — one framebuffer, refractive glass, rain
          as backdrop. Terminal cells stay in a DOM hole (xterm). No
          html-in-canvas / CEF capture path.
        </Text>
      </View>

      <View
        glass={glassPanel}
        style={{
          width: "stretch",
          padding: 16,
          radius: L.radius,
          background: rgba("#040a07", 0.55),
          direction: "column",
          gap: 8,
        }}
      >
        <Text style={{ fontSize: 14, fontWeight: 700, color: C.jadeSoft }}>
          Stack
        </Text>
        <Text style={{ fontSize: 12, color: C.mist400 }}>
          Tauri 2 · Vite · React 19 · Kussetsu · xterm.js (cell pane only)
        </Text>
        <Text style={{ fontSize: 12, color: C.mist400 }}>
          Needs WebGPU (Chrome 113+, Edge, Safari 18+, recent Firefox).
        </Text>
      </View>

      <View
        glass={glassPanel}
        style={{
          width: "stretch",
          padding: 16,
          radius: L.radius,
          background: rgba("#040a07", 0.55),
          direction: "column",
          gap: 8,
        }}
      >
        <Text style={{ fontSize: 14, fontWeight: 700, color: C.jadeSoft }}>
          Architecture rule
        </Text>
        <Text style={{ fontSize: 13, color: C.mist300 }}>
          Anything that isn’t shell output never snaps to a character cell.
          Smooth chrome on the GPU; honest grid only for the PTY surface.
        </Text>
      </View>
    </View>
  );
}
