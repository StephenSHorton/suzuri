import { createFileRoute } from "@tanstack/react-router"
import spec from "@/data/guest.v1.json"

export const Route = createFileRoute("/docs/protocol")({
  component: Page,
})

type Msg = { dir: string; fields: Record<string, string>; notes: string }

function Table({ group }: { group: Record<string, Msg> }) {
  return (
    <div className="mb-6 overflow-x-auto rounded-lg border border-border">
      <table className="w-full border-collapse text-left text-[0.82rem]">
        <thead>
          <tr className="border-b border-border text-mute">
            <th className="px-3 py-2 font-semibold">Message</th>
            <th className="px-3 py-2 font-semibold">Dir</th>
            <th className="px-3 py-2 font-semibold">Fields</th>
            <th className="px-3 py-2 font-semibold">Notes</th>
          </tr>
        </thead>
        <tbody>
          {Object.entries(group).map(([name, m]) => (
            <tr key={name} className="border-b border-border last:border-0">
              <td className="px-3 py-2 whitespace-nowrap text-accent">
                <code>{name}</code>
              </td>
              <td className="px-3 py-2 whitespace-nowrap">{m.dir}</td>
              <td className="px-3 py-2">
                {Object.keys(m.fields).length === 0
                  ? "—"
                  : Object.entries(m.fields).map(([k, v]) => (
                      <div key={k}>
                        <code>{k}</code>: {v}
                      </div>
                    ))}
              </td>
              <td className="px-3 py-2">{m.notes}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function Page() {
  return (
    <>
      <h1>Protocol</h1>
      <p>
        {spec.description} Protocol <code>{spec.version}</code>.
      </p>
      <p>
        Each line on the wire is one JSON object with a <code>type</code> field (the message
        name) plus the fields below. A minimal guest implements <code>spawn</code>,{" "}
        <code>navigate</code>, <code>kill</code>, and <code>hello</code> / <code>title</code>.
      </p>
      <h2>chrome → guest</h2>
      <Table group={spec.chromeToGuest} />
      <h2>guest → chrome</h2>
      <Table group={spec.guestToChrome} />
    </>
  )
}
