# Testing

The plugin has three test tiers, all driven from the `Makefile`. They are
deliberately separate: each has a different cost (free vs real money), a
different scope (Go function vs plugin contract), and different safety gates.

| Tier | Target | Hits real API? | Build tag | Scope |
|------|--------|----------------|-----------|-------|
| Unit (mock) | `make test` | No | (none) | Per-handler Go logic against fakes. |
| Live hcloud smoke | `make test-live-hcloud` | **Yes** | `integration` | Cheapest create/read/delete path via the hcloud Go SDK directly. |
| Conformance | `make conformance-test` | **Yes** | `conformance` | Full plugin contract (CRUD/inventory/sync/extract/discovery) through a real formae agent. |

The live and conformance suites are **mutually exclusive** by build tag (see
[Build tags](#build-tags) below). Passing one never compiles the other.

> **Never use a production project token** for any tier that hits the real
> API. Populate `.env` from `.env-sample` against a dedicated hcloud test
> project.

## 1. Unit tests (mock-only)

```
make test
```

Runs `go test ./...`. These tests live in `plugin/*_test.go` (no build tag)
and exercise each resource handler against in-process fakes of the hcloud
client. They never touch the network and are safe to run unconditionally.
This is the only tier run on every edit.

## 2. Live hcloud smoke tests

```
make test-live-hcloud        # preferred
make test-integration        # backwards-compatible alias for the above
```

These are **direct, provider-level smoke tests**. They call the hcloud Go SDK
straight from `plugin/*_integration_test.go` — they do **not** go through the
formae agent or the plugin binary. Their purpose is to confirm the cheapest
create/read/delete path for each managed resource type works at the API
level, independent of the plugin contract.

The exact command the target runs:

```
go test -tags=integration -run '^TestIntegration_' -count=1 -timeout=20m -v ./plugin/
```

### Safety gates (so a real hcloud account cannot leak)

1. **Double opt-in.** `TestMain` skips the whole suite (exit 0) unless **both**
   `HCLOUD_INTEGRATION=1` **and** a non-empty `HCLOUD_TOKEN` are set. The
   token value is never read or printed — only its non-emptiness is checked.
   The Make target loads `.env` and sets `HCLOUD_INTEGRATION=1` itself, so
   you do not have to set either var by hand.
2. **Sweep before and after.** `TestMain` runs a best-effort sweep
   (`sweep("pre-suite")` / `sweep("post-suite")`) that lists and deletes
   every hcloud resource labelled `managed_by=formae-integration-test` across
   all managed types. The pre-suite sweep recovers leaks from a prior crashed
   run; the post-suite sweep cleans up anything this run left behind.
3. **Per-test cleanup.** Every integration test must call `mustCleanup` the
   instant it creates a resource. It registers a `t.Cleanup` that issues the
   delete and **fails the test loudly** on error — a missed cleanup is a hard
   failure, not silent drift.
4. **Run-scoped labels.** Every created resource carries
   `{managed_by: "formae-integration-test", run: <unix-time>}` so concurrent
   runs do not collide. The pre-suite sweep deliberately matches only
   `managed_by` so it recovers leaks from any prior run regardless of run ID.

The label `managed_by=formae-integration-test` is **distinct** from the
conformance suite's `owner=formae-conformance-test` label — the two suites
clean up after themselves independently and never interfere.

## 3. Conformance tests (official harness)

```
make conformance-test                       # CRUD + discovery
make conformance-test-crud                  # CRUD lifecycle only
make conformance-test-discovery             # discovery only
make conformance-test-crud TEST=ssh-key     # single-resource filter
```

These drive the **real plugin binary end-to-end** via the official formae
conformance SDK: `formae apply` / `inventory` / `extract` / `sync` /
`destroy`. The harness boots a real formae agent (downloaded via orbital
unless `FORMAE_BINARY` points at a local binary), discovers the installed
plugin from `~/.pel/formae/plugins`, and walks the Pkl fixtures in
`plugin/testdata/`.

The two test functions and their `-run` filters:

| Function | `-run` filter | What it exercises |
|----------|---------------|--------------------|
| `TestPluginConformance` | `^TestPluginConformance$` | CRUD lifecycle per fixture. |
| `TestPluginDiscovery` | `TestPluginDiscovery$` | Inventory sync + OOB discovery. |

A single resource type (or comma-separated list) is selected with the
`TEST=` Make variable, which is forwarded as `FORMAE_TEST_FILTER`.

### Prerequisites

- `make install` first (conformance targets depend on it).
- `HCLOUD_TOKEN` set — enforced by the `setup-credentials` prerequisite,
  which loads `.env` and fails fast on an empty token. (The credential check
  is implemented inline in the Makefile; no shell script is shipped.)
- A dedicated hcloud test project. **Never** a production token.

### Timeouts

hcloud is a fast backend, so the per-phase defaults are intentionally lower
than the kube-style defaults used by other formae plugins. Override any of
them on the `make` invocation.

| Var | Default | Bounds |
|-----|---------|--------|
| `TIMEOUT` | `30m` | Outer `go test -timeout`. Bare digits are treated as minutes. Must exceed the sum of the inner budgets. |
| `FORMAE_TIMEOUT` | `10` (min) | Framework per-operation wait (`PollStatus` / `WaitForResourceCompletion`). |
| `OOB_TIMEOUT` | `10` (min) | A single OOB Create/Delete plugin RPC during the discovery `CreateOOB` phase. |
| `OOB_DELETE_TIMEOUT` | `5` (min) | Post-sync inventory tombstone wait after the plugin `Delete` returns. |
| `DISCOVERY_TIMEOUT` | `10` (min) | Inventory polling window during the discovery wait for an unmanaged resource. |

### Cleanup

`make clean-environment` (alias: `make conformance-cleanup`) sweeps every
hcloud resource labelled `owner=formae-conformance-test`, in dependency order
(servers first, ssh-keys/certificates last; images last since only snapshots
carry user labels). It is:

- **Idempotent** — safe to run multiple times. Missing resources do not fail.
- **Best-effort** — per-resource delete failures are logged but do not abort
  the sweep, so one stuck resource cannot shield the rest.
- **Self-contained** — implemented inline in the Makefile. It sources `.env`,
  warns and skips if `HCLOUD_TOKEN` is empty or the `hcloud` CLI is missing,
  and otherwise loops over the managed hcloud sub-commands. **No cleanup
  scripts ship with this repo.**

There is no longer an automated pre/post hook around the conformance
targets — run `make clean-environment` by hand before and/or after a
conformance run if you want a clean slate.

## Build tags

| File(s) | Build tag | Meaning |
|---------|-----------|---------|
| `plugin/*_test.go` | (none) | Always compiled. Mock-only unit tests. |
| `plugin/*_integration_test.go` | `//go:build integration && !conformance` | Direct live hcloud API smoke tests. Excluded from `go test ./...` and compiled out when `conformance` is set. |
| `plugin/conformance_test.go` | `//go:build conformance && !integration` | formae conformance harness. Compiled out when `integration` is set. |

The `&& !<other>` half guarantees that even if **both** tags are passed the
two live suites are never compiled together — they would otherwise both
define `TestMain` and collide.

## Conformance fixtures

`plugin/testdata/*.pkl` are **conformance-only** inputs — see
[`plugin/testdata/README.md`](../plugin/testdata/README.md). The live hcloud
smoke tests build their inputs inline in Go and never read that directory.

### `HETZNER::Compute::Image` caveat

formae only **owns** snapshots: a snapshot is created from an existing server
(`Server.CreateImage`) and is otherwise read/deleted. The hcloud API only
supports creating snapshots via `CreateImage`, so any other `type` is
rejected. Image creation is therefore **excluded from the conformance
fixtures** (it requires composing a snapshot with a live server), so only 10
of the 11 managed resource types are exercised by `make conformance-test`.
The unit tests cover the Image lifecycle against mocks.
