import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/privacy")({
  component: PrivacyPage,
})

function PrivacyPage() {
  return (
    <main className="legal mx-auto w-[min(42rem,92vw)] pt-8 pb-16 leading-relaxed">
      <h1 className="font-serif text-[1.75rem]">Privacy Policy</h1>
      <p className="mb-6 text-[0.85rem] text-mute">
        Last updated: 2026-08-06 · Publisher: Horton Software LLC
      </p>
      <p>
        This policy describes how <strong>suzuri</strong> (“the app”), published by{" "}
        <strong>Horton Software LLC</strong>, handles information when you install and use the
        app from the Microsoft Store or other distribution channels.
      </p>
      <h2 className="mt-7 mb-2 text-[1.05rem]">Summary</h2>
      <p>
        suzuri is a local terminal host. It does <strong>not</strong> require an account, does{" "}
        <strong>not</strong> sell personal data, and does <strong>not</strong> include advertising
        or third-party analytics SDKs.
      </p>
      <h2 className="mt-7 mb-2 text-[1.05rem]">Data stored on your device</h2>
      <ul className="pl-5">
        <li>
          <strong>Settings and preferences</strong> (theme, font, profiles, window placement)
          under your user config directory (e.g. <code>%LOCALAPPDATA%\suzuri</code> on Windows).
        </li>
        <li>
          <strong>Optional notes</strong> you create in the app, stored locally.
        </li>
        <li>
          <strong>Diagnostic logs</strong> written locally to help debug crashes (e.g.{" "}
          <code>suzuri.log</code>). Logs stay on your machine unless you choose to share them.
        </li>
      </ul>
      <p>
        Shell session content is handled by your local shell and programs you run; suzuri does
        not upload your terminal scrollback to Horton Software.
      </p>
      <h2 className="mt-7 mb-2 text-[1.05rem]">Network activity</h2>
      <ul className="pl-5">
        <li>
          <strong>Microsoft Store builds:</strong> updates are delivered by the Microsoft Store.
          The app does not self-update from GitHub.
        </li>
        <li>
          <strong>Non-Store builds:</strong> optional update checks may contact GitHub Releases.
          GitHub may log IP addresses per its policies.
        </li>
        <li>
          <strong>Optional features:</strong> peer-to-peer file transfer and MCP diagnostics are
          user-initiated.
        </li>
        <li>Opening links uses your default browser and those sites’ policies.</li>
      </ul>
      <h2 className="mt-7 mb-2 text-[1.05rem]">What we do not collect</h2>
      <ul className="pl-5">
        <li>No telemetry or crash reporting to Horton Software servers by default.</li>
        <li>No advertising identifiers or marketing trackers.</li>
        <li>No sale of personal information.</li>
      </ul>
      <h2 className="mt-7 mb-2 text-[1.05rem]">Children</h2>
      <p>The app is a general-purpose developer tool and is not directed at children under 13.</p>
      <h2 className="mt-7 mb-2 text-[1.05rem]">Changes</h2>
      <p>
        We may update this policy; the “Last updated” date above will change when we do.
        Continued use after changes means you accept the revised policy.
      </p>
      <h2 className="mt-7 mb-2 text-[1.05rem]">Contact</h2>
      <p>
        Horton Software LLC
        <br />
        Website:{" "}
        <a className="text-accent" href="https://hortonsoftware.com">
          https://hortonsoftware.com
        </a>
        <br />
        Support:{" "}
        <a className="text-accent" href="mailto:support@hortonsoftware.com">
          support@hortonsoftware.com
        </a>
      </p>
    </main>
  )
}
