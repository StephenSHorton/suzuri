import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/docs/install")({
  component: Page,
})

function Page() {
  return (
    <>
      <h1>Install a guest</h1>
      <p>Suzuri looks for manifests in the product config directory:</p>
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
