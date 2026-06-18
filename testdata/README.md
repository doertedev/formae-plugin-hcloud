# testdata/

This directory holds **conformance fixtures only** — Pkl `forma` files consumed
by the formae conformance harness via `conformance_test.go` (build tag
`conformance`).

Each fixture (`*.pkl`) declares a single, cheapest-possible resource of one
managed type (e.g. `ssh-key.pkl`, `network.pkl`) so the harness can exercise
the full plugin contract (create / read / update / delete / inventory / sync /
extract) end-to-end through `formae apply` / `inventory` / `extract` / `sync` /
`destroy`. Fixtures source shared config (stack, target, run-scoped IDs) from
`config/`.

## What does NOT live here

- These are **not** inputs to the direct live hcloud API smoke tests. Those
  tests live in `pkg/*_integration_test.go` (build tag `integration`), call
  the hcloud Go SDK directly, and build their inputs inline in Go — they never
  read this directory.
- These are **not** golden/canned responses for unit tests either. The
  in-process unit/mock tests in `pkg/*_test.go` construct their inputs in
  Go as well.

## Layout

```
testdata/
  config/            shared Pkl config (stack, target, testRunID, ...)
  *.pkl              one fixture per managed resource type
  *-update.pkl       optional patch-mode update variant for a base fixture
  *-replace.pkl      optional patch-mode replacement variant for a base fixture
  PklProject         Pkl project manifest
  PklProject.deps.json
```

Variant files are discovered by filename convention: `network.pkl` may have
`network-update.pkl` and/or `network-replace.pkl` beside it. Update variants
must keep the same resource label and avoid create-only changes so the harness
can assert the NativeID stays stable. Replace variants keep the same label but
change a create-only field so the harness can assert the NativeID changes.

Only selected cheap resources have variants. Slow or awkward resources can
remain base-only when a replacement would add disproportionate runtime or
fixture complexity.
