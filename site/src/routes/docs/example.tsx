import { createFileRoute, Link } from "@tanstack/react-router"

export const Route = createFileRoute("/docs/example")({
  component: Page,
})

function Page() {
  return (
    <>
      <h1>Example plugin</h1>
      <p>
        The example guest shows a title, accepts <code>navigate</code>, and echoes the URL.
      </p>
      <p>
        Point a <Link to="/docs/manifest">manifest</Link> at the example binary, restart suzuri,
        and open a guest pane from the palette. See{" "}
        <Link to="/docs/install">install</Link> for the config paths.
      </p>
    </>
  )
}
