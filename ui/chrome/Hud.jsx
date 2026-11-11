// The stats HUD (Cmd/Ctrl+Shift+H): a small Astryx Card in the bottom right
// corner, one line of code-font Text per stat line. app.js gathers the
// numbers and calls showHud(lines) / hideHud().
import { Card } from "@astryxdesign/core/Card"
import { Text } from "@astryxdesign/core/Text"
import { VStack } from "@astryxdesign/core/VStack"
import { mountOverlay, unmountOverlay } from "./overlays.js"
import "./hud.css"

function Hud({ lines }) {
  return (
    <div className="rsz-hud">
      <Card padding={2} elevation="med">
        <VStack gap={0.5}>
          {lines.map((line, i) => (
            <Text key={i} type="code" color="secondary" display="block">
              {line}
            </Text>
          ))}
        </VStack>
      </Card>
    </div>
  )
}

export function showHud(lines) {
  mountOverlay("hud", <Hud lines={lines} />)
}

export function hideHud() {
  unmountOverlay("hud")
}
