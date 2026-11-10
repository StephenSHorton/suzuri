// The keyboard shortcuts sheet (ui/help.js) as an Astryx Dialog: the
// sections from helpSections() in a grid that is two columns when there is
// room, each chord as Kbd badges (kbdParts() turns the label into keys).
// help.js owns the keys (Esc / Enter / the help chord close it); this view
// only handles the close button and a click on the backdrop.
import { Dialog, DialogHeader } from "@astryxdesign/core/Dialog"
import { Layout, LayoutContent, LayoutFooter } from "@astryxdesign/core/Layout"
import { Grid } from "@astryxdesign/core/Grid"
import { HStack, VStack } from "@astryxdesign/core/Stack"
import { Heading } from "@astryxdesign/core/Heading"
import { Text } from "@astryxdesign/core/Text"
import { Kbd } from "@astryxdesign/core/Kbd"
import { kbdParts } from "../help.js"

function Keys({ label, isMac }) {
  return (
    <HStack gap={1} align="center" wrap="wrap" className="rsz-help-keys">
      {kbdParts(label, isMac).map((p, i) =>
        p.kbd ? <Kbd key={i} keys={p.kbd} />
        : p.sep ? <Text key={i} type="supporting">·</Text>
        : <Text key={i} type="supporting">{p.text}</Text>,
      )}
    </HStack>
  )
}

function Section({ section, isMac }) {
  return (
    <VStack gap={1.5}>
      <Heading level={3} color="secondary">{section.title}</Heading>
      {section.rows.map((r) => (
        <HStack key={r.desc} gap={3} align="center">
          <Keys label={r.keys} isMac={isMac} />
          <Text>{r.desc}</Text>
        </HStack>
      ))}
    </VStack>
  )
}

export function HelpSheet({ isOpen, sections, isMac, onClose }) {
  return (
    <Dialog isOpen={isOpen} onOpenChange={(open) => { if (!open) onClose() }} width={800} maxHeight="85dvh">
      <Layout
        header={<DialogHeader title="Keyboard shortcuts" onOpenChange={() => onClose()} />}
        content={
          <LayoutContent>
            <Grid columns={{ minWidth: 320, max: 2 }} columnGap={6} rowGap={5} align="start">
              {sections.map((s) => <Section key={s.title} section={s} isMac={isMac} />)}
            </Grid>
          </LayoutContent>
        }
        footer={
          <LayoutFooter>
            <HStack gap={1.5} align="center">
              <Kbd keys="esc" />
              <Text type="supporting">close</Text>
            </HStack>
          </LayoutFooter>
        }
      />
    </Dialog>
  )
}
