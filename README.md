# aether-edit

Transcoder with some bells: a media transcode and edit platform, built as a
monorepo with Go services, a Rust renderer path, and TypeScript clients. This
repository currently contains the WO-001R scaffold: the directory layout,
pinned toolchains, LF enforcement, and the CI gate. Functional code arrives in
later work orders, starting with WO-014.

## Monorepo map

| Path | Contents |
|---|---|
| `schema/` | Canonical data and message schemas (content: WO-014) |
| `packages/core/` | Shared client core library (content: WO-014) |
| `packages/web/` | Web client (content: WO-014) |
| `packages/mobile/` | Mobile client (content: WO-014) |
| `packages/console/` | Operator console (content: WO-014) |
| `services/api/` | API service, Go (content: WO-014) |
| `services/orchestrator/` | Orchestrator service, Go (content: WO-014) |
| `services/renderer/` | Renderer service (content: WO-014) |
| `infra/` | Infrastructure; `infra/ci/` holds the CI check scripts (live now) |
| `docs/` | Documentation; `docs/design/` holds design material, with third-party trees kept byte-exact |
| `resume.txt` | Pointer only; the operational log lives outside the repo for now |

## Toolchains (pinned)

- **Node**: `.nvmrc` pins the LTS major (currently 24). Use `nvm use` or any
  `.nvmrc`-aware tool. GitHub Actions reads the same file and is the authority.
- **Rust**: `rust-toolchain.toml` pins stable 1.89.0 with clippy and rustfmt;
  rustup picks it up automatically.
- **Go**: pinned per service via the `toolchain` directive in each service's
  `go.mod` (currently `go1.25.12`). There is no repo-global Go pin file; the
  `go.mod` toolchain directive is the single source of truth per module.

Line endings are LF everywhere, enforced by `.gitattributes` and by CI
(`infra/ci/check-lf.sh`). No CRLF, no BOMs, with the single exception of
third-party trees under `docs/design/ui_kits/`, which are preserved byte-exact.

## Bootstrap

```sh
git clone https://github.com/evemeta-tony/aether-edit.git
cd aether-edit
bash infra/ci/check-all.sh
```

That third command runs exactly the checks CI runs. A fresh clone plus this
command equals green CI: on the scaffold it reports the tree clean and the
lint gates armed but with nothing to lint yet. No toolchain installation is
required until source files for that toolchain exist.

## Workflow

All change lands through numbered work orders (WOs), each with a fixed scope,
its own branch, and a PR whose title starts with the WO id. CI must be green
before merge, and anything outside a WO's declared scope moves to a later WO
instead of riding along.
