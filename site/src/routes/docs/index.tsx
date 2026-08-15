import { createFileRoute, Link } from "@tanstack/react-router"

export const Route = createFileRoute("/docs/")({
  component: DocsIntro,
})

function DocsIntro() {
  return (
    <>
      <h1>Guests</h1>
      <p>
        Suzuri is a native terminal host. A <strong>guest</strong> is an optional process it can
        place in the mosaic next to a terminal or workspace. Suzuri does not ship engines. You
        install a guest yourself and drop a manifest under the product config directory.
      </p>
      <h2>What a pane is</h2>
      <p>
        A leaf in the split tree: a terminal, or a widget (workspace or guest). Same sashes,
        tear-off, and focus. The well is not always cells.
      </p>
      <h2>In these docs</h2>
      <ul>
        <li>
          <Link to="/docs/install">Install</Link> — where manifests live
        </li>
        <li>
          <Link to="/docs/manifest">Manifest</Link> — the JSON file next to a guest binary
        </li>
        <li>
          <Link to="/docs/protocol">Protocol</Link> — messages on the localhost channel
        </li>
        <li>
          <Link to="/docs/example">Example plugin</Link> — a minimal guest
        </li>
      </ul>
    </>
  )
}
