# Development

This is the developer guide for working on `formae-plugin-hcloud` locally.
There is no CI pipeline in this repo (no `.github/workflows/`); everything
here is run by hand on a developer machine. See [testing.md](testing.md) for
the test tiers and [packaging.md](packaging.md) for build/publish.

## Guide map

- [schema.md](schema.md) covers the schema layout, resource hints, field hints,
  conformance fixtures, and extractability caveat.
- [schema-data-types.md](schema-data-types.md) is the copy-paste reference for
  Pkl data shapes used by this plugin: scalars, enums, mappings, listings,
  nested classes, provider-default fields, resolvables, and resource fragments.
- [testing.md](testing.md) covers unit tests, live hcloud smoke tests, and
  conformance runs.
- [packaging.md](packaging.md) covers install, `.opkg` packaging, publishing,
  and schema package publishing.

## Prerequisites

| Tool | Version | Why |
|------|---------|-----|
| Go | `1.26.0`+ (see `go.mod`) | Build the plugin binary and run tests. |
| `pkl` CLI | `0.30.0`+ | Read `formae-plugin.pkl` in the Makefile and validate the schema (`make pkl-eval`). The Makefile enforces this for Pkl-dependent targets via `make check-pkl`. |
| `hcloud` CLI | optional | Only needed for `make clean-environment` (the conformance labelled-resource sweep). |
| `golangci-lint` | optional | Only needed for `make lint`. |
| pel/orbital `ops` | optional | Only needed for `make pkg` / `make publish`. See [packaging.md](packaging.md). |

There is **no** expectation of GitHub Actions, container build, or shared CI
runner. The Make targets are the canonical entry points.

## Environment variables

The plugin resolves its hcloud API token via `pkg/plugin.go::resolveToken`:

1. A `token` field in the formae target config JSON (`{"token":"..."}`) wins.
2. Otherwise the `HCLOUD_TOKEN` environment variable is consulted.
3. Empty/null config falls back to the env var; a malformed config (genuine
   JSON parse error) is surfaced as an error rather than silently falling
   through.

For local development, copy `.env-sample` to `.env` (gitignored) and populate
it. The Makefile's live/conformance targets source `.env` automatically:

```
cp .env-sample .env
# edit .env: HCLOUD_TOKEN="<your hcloud project token>"
```

| Var | Used by | Notes |
|-----|---------|-------|
| `HCLOUD_TOKEN` | plugin runtime, live tests, conformance, cleanup | Required for anything hitting the real API. **Never** use a production project token. |
| `HCLOUD_INTEGRATION` | live hcloud smoke tests | Double-opt-in gate for `TestMain`. The `test-live-hcloud` Make target sets this for you. |
| `HCLOUD_S3_ACCESS_KEY` / `HCLOUD_S3_SECRET_KEY` | (reserved) | Present in `.env-sample` for certificate-related fixtures that need object storage; not required for the core suite. |

`.env` is gitignored. Do not commit tokens.

## Build & install layout

```
make build     # compiles ./bin/formae-plugin-hcloud
make install   # installs into ~/.pel/formae/plugins/<name>/v<version>/
```

The formae agent's plugin discovery (`pkg/plugin/discovery/discovery.go` in
the formae tree) requires a **versioned** layout — without it the agent never
sees the plugin and apply/conformance fail with
`timeout waiting for plugin <NS> to register`:

```
<PLUGINS_DIR>/<name>/v<semver>/<name>            (binary, named exactly <name>)
<PLUGINS_DIR>/<name>/v<semver>/formae-plugin.pkl (manifest)
<PLUGINS_DIR>/<name>/v<semver>/schema/pkl/...    (schema)
```

`<name>` and `<version>` are read from `formae-plugin.pkl` via `pkl eval`, so
the Makefile never hardcodes them. Override the base dir per-invocation:

```
make install PLUGINS_DIR=/tmp/plugins
```

`make install` wipes any prior install of this plugin name (and the legacy
flat `HETZNER/` layout) first, so each install is a clean slate.

## Common Make targets

Run `make help` for the authoritative, always-up-to-date list. The everyday
ones:

| Target | What it does |
|--------|--------------|
| `make build` | Compile `./bin/formae-plugin-hcloud`. |
| `make install` | Build + install into the versioned plugin layout. |
| `make test` | Mock-only `go test ./...`. Never hits the real API. Safe to run anytime. |
| `make test-live-hcloud` | Direct live hcloud API smoke tests. Opt-in, real API. See [testing.md](testing.md). |
| `make vet` | `go vet ./...`. |
| `make lint` | `golangci-lint run ./...` (fails if the linter is missing). |
| `make pkl-eval` | Validate `formae-plugin.pkl` and `schema/pkl/**/*.pkl` evaluate. |
| `make pkg-pkl` | `pkl project package schema/pkl` → `.out/`. |
| `make clean` | Remove `./bin/`, `.out/`, `dist/`. |
| `make tidy` | `go mod tidy`. |

Conformance and packaging targets are documented in [testing.md](testing.md)
and [packaging.md](packaging.md) respectively.

## Typical local loop

```
make vet && make test          # fast feedback against mocks
make pkl-eval                  # if you touched schema/pkl/
make build && make install     # stage the binary the agent will discover
make conformance-test-crud TEST=ssh-key   # exercise one type end-to-end (real API)
```

## Adding a resource type

A new resource type drops in without editing `pkg/plugin.go`:

1. **Schema** — add `schema/pkl/<category>/<type>.pkl` declaring a `Resource`
   subclass annotated with `@hcloud.ResourceHint` and per-field
   `@hcloud.FieldHint`s. See [schema.md](schema.md), and use
   [schema-data-types.md](schema-data-types.md) for copy-pasteable examples
   of each Pkl field shape.
2. **Handler** — add `pkg/<type>.go` implementing the `resourceHandler`
   interface (`create`/`read`/`update`/`delete`/`list`) and self-register it
   via `register("HETZNER::...::Type", h)` in an `init()`.
3. **Tests** — add `pkg/<type>_test.go` (mock-based unit tests) and
   `pkg/<type>_integration_test.go` (live hcloud smoke, build tag
   `integration && !conformance`).
4. **Conformance fixture** — add `testdata/<type>.pkl` so the
   conformance harness exercises the new type. See [testing.md](testing.md)
   for the conformance fixture contract.
