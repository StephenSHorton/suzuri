// The notice panel's cards (notice.js mounts this in its own window; the
// window's size and place come from app/src/notices.rs). One ClickableCard per
// notice: click focuses the pane that asked, right click or the close button
// dismisses. Oldest first; notices.css stacks them bottom-up.
import { ClickableCard } from "@astryxdesign/core/ClickableCard"
import { HStack } from "@astryxdesign/core/HStack"
import { VStack } from "@astryxdesign/core/VStack"
import { Text } from "@astryxdesign/core/Text"
import { IconButton } from "@astryxdesign/core/IconButton"
import { Icon } from "@astryxdesign/core/Icon"

// notices.rs CARD_W x CARD_H: it sizes the window for exactly these.
const CARD_W = 320
const CARD_H = 72

function NoticeCard({ card, click, dismiss }) {
  const title = card.title || card.body
  const body = card.title ? card.body : ""
  return (
    // Not Astryx: a wrapper for the accent bar, which Astryx has no part for
    // (Card's colour variants tint the whole card, which its docs reserve for
    // categories; Toast has neither a title nor a warning tone). The wrapper
    // also carries the entrance animation, see notices.css.
    <div className="rsz-notice" data-tone={card.tone || undefined}>
      <ClickableCard
        label={title}
        width={CARD_W}
        height={CARD_H}
        padding={0}
        onClick={() => click(card.key)}
        onContextMenu={(e) => {
          e.preventDefault()
          dismiss(card.key)
        }}
      >
        <HStack height="100%" gap={1} vAlign="center" paddingInlineStart={4} paddingInlineEnd={2}>
          {/* min-width 0 lets the one-line Text truncate inside the row. */}
          <VStack gap={1} className="rsz-notice-text">
            {/* No truncation tooltips: the window is only as big as its cards. */}
            <Text type="label" weight="semibold" maxLines={1} hasTruncateTooltip={false}>
              {title}
            </Text>
            {body && (
              <Text type="supporting" maxLines={1} hasTruncateTooltip={false}>
                {body}
              </Text>
            )}
          </VStack>
          {/* No tooltip, for the same reason; aria-label names it. */}
          <IconButton
            label="Dismiss notice"
            icon={<Icon icon="close" />}
            variant="ghost"
            size="sm"
            onClick={() => dismiss(card.key)}
          />
        </HStack>
      </ClickableCard>
    </div>
  )
}

export function Notices({ cards, click, dismiss }) {
  return (
    // Not Astryx: VStack has no reversed direction; see #stack in notices.css.
    <div id="stack">
      {cards.map((c) => (
        <NoticeCard key={c.key} card={c} click={click} dismiss={dismiss} />
      ))}
    </div>
  )
}
