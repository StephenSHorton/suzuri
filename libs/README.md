# libs — first-party engines and libraries

Code that implements suzuri features but is **not** the Go host. One folder per library.

| Lib | Language | Role | Shipped binary |
|-----|----------|------|----------------|
| [`transfer/`](transfer/) | Rust | P2P file transfer (iroh) | `suzuri-transfer` |

## Rules

1. **Product is suzuri** — no second GitHub “app” for a feature.
2. **One feature → one directory** under `libs/<name>/` with a short README.
3. **Host talks over a stable boundary** (today: NDJSON sidecar). Prefer that over mixing languages in one process unless necessary.
4. **Sidecar binary names:** `suzuri-<name>` (e.g. `suzuri-transfer`).
5. **Do not use `vendor/`** for checkouts — Go treats that as modules vendor.
6. **Release** must build every shipped sidecar from this tree (see `.github/workflows/release.yml`).

## Adding a lib

- [ ] Create `libs/<name>/` with language-native project files
- [ ] Document how the host invokes it
- [ ] Wire CI test + release package
- [ ] Resolve path next to `suzuri` (or `SUZURI_*_BIN`)
- [ ] User-facing docs under `docs/` and README feature table if needed
