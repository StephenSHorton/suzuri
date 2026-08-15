import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/docs/manifest")({
  component: Page,
})

function Page() {
  return (
    <>
      <h1>Manifest</h1>
      <p>A guest is a binary plus a JSON file:</p>
      <pre className="overflow-x-auto rounded-lg border border-border bg-panel p-4 text-[0.82rem] text-foreground">
        {`{
  "id": "example",
  "name": "Example",
  "command": "/path/to/suzuri-guest-example",
  "protocol": 1,
  "capabilities": ["pane", "navigate"]
}`}
      </pre>
      <p>
        <code>protocol</code> must match the reference on the protocol page. Unknown capability
        strings are ignored so older suzuri can still spawn newer guests.
      </p>
    </>
  )
}
