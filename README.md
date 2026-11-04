# Dray

Dray deploys code from Git to its targets — Kubernetes through GitOps, Piral
feeds, and more — for people and for AI agents, without either holding cloud
credentials. Every deploy is a plan, then an apply, pinned to one commit;
production waits for a person's passkey.

This branch is Dray v2, a new build in Go. It replaces the JS `dray` CLI
(`@brutalsystems/dray`, on `main`) once it reaches parity.

**Status:** milestone M0.1, the skeleton. Nothing deploys yet.

## Build

Requires Go 1.26 (pinned in `.tool-versions`).

```bash
make build      # bin/dray, bin/dray-server
make test       # go vet + go test -race
make lint       # golangci-lint
make snapshot   # goreleaser: every platform into dist/
```

## Layout

| Path | What |
| --- | --- |
| `cmd/dray` | The CLI |
| `cmd/dray-server` | The server (MCP, HTTP API, approvals), from milestone M1.2 |
| `internal/cli` | Command tree shared by both binaries |
| `internal/version` | Version info, set at release time |
| `docs/decisions.md` | Why Dray is built the way it is |

## License

MIT
