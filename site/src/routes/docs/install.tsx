import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/docs/install")({
  component: Page,
})

function Page() {
  return (
    <>
      <h1>Install a guest</h1>
      <p>
        Ladybird is the first catalog guest. It is not inside the Suzuri app.
        Install the helper, then open a pane:
      </p>
      <pre>
        suzuri guest install ladybird{"\n"}
        suzuri guest remove ladybird{"\n"}
        suzuri guest list
      </pre>
      <p>
        <code>install</code> downloads a helper release when one exists, or you
        can point at a local <code>Ladybird.app</code> with{" "}
        <code>--from</code>. That writes a manifest under the product config
        directory. Palette → <strong>Guests</strong> for the same install /
        remove card, or <strong>New guest pane</strong> to open one.
      </p>
      <p>Suzuri looks for manifests here:</p>
      <ul>
        <li>
          macOS: <code>~/Library/Application Support/suzuri/guests/*.json</code>
        </li>
        <li>
          Windows: <code>%LOCALAPPDATA%\suzuri\guests\</code>
        </li>
        <li>
          Linux: <code>~/.config/suzuri/guests/</code>
        </li>
      </ul>
      <p>
        Missing guests are a soft no-op. The app still launches. Palette entries appear only
        for manifests that resolve to a binary on disk.
      </p>
    </>
  )
}
