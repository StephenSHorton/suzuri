import { createFileRoute, Link } from "@tanstack/react-router"
import { MS_STORE } from "@/lib/site"

export const Route = createFileRoute("/docs/faq")({
  component: Page,
})

function Page() {
  return (
    <>
      <h1>FAQ</h1>
      <h2>What is suzuri?</h2>
      <p>
        A native terminal host for Windows and macOS. It owns the window, the PTY, and the
        chrome. Shells run for real — PowerShell, cmd, zsh, bash.
      </p>
      <h2>Does it need an account?</h2>
      <p>
        No. Settings stay on your machine. See the{" "}
        <Link to="/privacy">privacy policy</Link>.
      </p>
      <h2>How do I install on Windows?</h2>
      <p>
        Use the{" "}
        <a href={MS_STORE}>Microsoft Store listing</a>. That is the signed Windows install.
        GitHub <code>.exe</code> builds are unsigned.
      </p>
      <h2>Is Linux supported?</h2>
      <p>Not as a host build. Manifests can still live under the Linux config path.</p>
      <h2>What is a guest?</h2>
      <p>
        An optional process you install and point at with a manifest. Suzuri can place it in
        the mosaic. It does not ship inside the app.
      </p>
      <h2>Can I write my own guest?</h2>
      <p>
        Yes. Implement the <Link to="/docs/protocol">protocol</Link>, drop a{" "}
        <Link to="/docs/manifest">manifest</Link>, restart suzuri.
      </p>
    </>
  )
}
