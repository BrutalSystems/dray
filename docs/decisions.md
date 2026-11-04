# Decisions

Each entry records a decision, why it was made, and what it rules out. Newest
last.

## D1 · A new build in Go, keeping the old tool's behavior

Dray v2 is written in Go: core, CLI and server in one module, two binaries
(`dray`, `dray-server`). It ports the behavior of the JS dray (v0.1.16), not
its code; the old tool's safety rules become acceptance tests. Go gives single
static binaries with nothing to install, and Kubernetes, Argo CD, BuildKit,
OpenBao, Kyverno and kubeconform are Go libraries Dray can call directly.

## D2 · Name and repo

The name stays Dray. Module path `github.com/BrutalSystems/dray`: v2 will take
over that repo. Until it reaches parity it lives on the orphan branch `v2`,
alongside the JS tool on `main`; the takeover makes `v2` the default branch.

## D3 · Per-repo config is `.dray/config.yaml`

Same four sections as the JS tool (images, workloads, secrets, pilets).
`dray config migrate` converts `.dray/config.json`.

## D4 · Every deploy is a plan, then an apply, pinned to a commit

Images are referenced by digest, never by tag, including CronJobs (no
`:latest` exception). Production changes need a passkey on Dray's approval
page; a yes in chat, a chat-app reply or a push-notification button never
approves on its own.

## D5 · The secret store is swappable; OpenBao is the default

Core depends on a `Store` interface. Repos and registry entries use logical
`secret:<scope>/<name>` references, never store paths. Adapters: `openbao`
(default), `aws-secrets-manager`, `file`, `sops` (read-only, migration). Core
features never require dynamic credentials.

## D6 · Cloud access through each cloud's workload identity

No cloud key is stored anywhere, the secret store included. A `CloudIdentity`
adapter per cloud: `aws` first (EKS Pod Identity), `azure` and `gcp` later.
Cross-cloud access federates the cluster's service-account tokens.

## D7 · Dray mints its own GitHub tokens

The GitHub App's private key is an ordinary secret in the configured store.
Dray mints installation tokens scoped to one job; only `dray-server` can read
the key; the key rotates on a schedule.

## D8 · Dray issues its own MCP tokens; sign-in through OIDC

Dray runs a small OAuth server for MCP clients and delegates sign-in to any
OIDC provider. Per-session tokens can be listed and revoked. Nothing
provider-specific lives in Dray's code: issuer URLs, client IDs and group
names belong in deployment config.

## D9 · Agent code reaches Dray only as a pushed GitHub branch

Dray builds a commit after CI has run on it. No code uploads. Operators keep
local dirty builds in dev through the CLI.

## D10 · Dray's database is its own Postgres instance

Reached with IAM database auth through the cloud-identity adapter (RDS on
AWS). Audit events are also shipped to an external log store as a copy the
database cannot edit. An adopter only needs a Postgres connection string.

## D11 · Lessons from the JS tool's real deployments

- `plan_deploy` takes a component selector, so one workload of a repo with
  many can ship alone; `manual` and `disabled` stay in the config.
- Targets span several cloud accounts and clusters; the cloud-identity adapter
  assumes a role in each target account, and approvers can be set per app.
- Onboarding creates container repositories with immutable tags and scan on
  push; retention never deletes a digest that is live or in recent deploy
  history.
- Pilets publish through the repo's own command at first; later, pack plus
  Dray's publish for hash-checked promotion, with a custom command kept as an
  escape hatch.
- `dray config migrate` flags environments faked as duplicate workloads for a
  person to map; early milestones assume one environment per app.
- One owner per cluster, namespace and workload; the reconcile loop flags a
  second claim.
- An image already built for a commit is reused, not rebuilt.
- Builders run on arm64 when the clusters do.

## D12 · Scope kept for later phases

Preview environments per pull request, release groups for services that ship
together, and an S3 + CloudFront target for static sites.
