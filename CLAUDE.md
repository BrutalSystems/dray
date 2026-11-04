# Dray v2

A new build, in Go, of the dray deploy tool. The JS tool is on the `main`
branch (v0.1.16); port its behavior, not its code.

**This repo is public.** Never commit anything specific to one organization:
company or client names, internal app names, cloud account IDs, cluster names,
internal URLs, identity-provider details, or links to private documents. Those
belong in deployment config, kept outside this repo. Private pointers for a
local session go in `CLAUDE.local.md`, which is gitignored.

## Rules that shape the code

- Dray must stand on its own. Every external system sits behind an interface
  (secret store, cloud identity, builder, target, Git); core never imports a
  provider SDK.
- Repos use logical `secret:` references, never store paths. Everything an app
  needs to deploy is in its `.dray/config.yaml` (D20); credentials never are.
- Decisions already made are in `docs/decisions.md`. Don't reopen one without
  saying so.
- The JS tool's safety rules are acceptance tests. Each gets a test when it is
  ported.

## Commands

- `make test` — `go vet` and `go test -race`
- `make lint` — golangci-lint v2 (`.golangci.yml`)
- `make snapshot` — goreleaser build for every platform

Go 1.26, pinned in `.tool-versions`.
