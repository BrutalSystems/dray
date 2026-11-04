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

## D13 · Front ends with build-time settings: per-environment builds now, runtime settings over time

Bundlers such as Vite write environment values into the bundle at build time,
so promoting one image from staging to prod would ship staging's settings.
An image that declares per-environment build settings is rebuilt for each
environment from the same commit, and the plan says "rebuilt", not
"promoted". The goal for every front end is runtime settings (a config file
written at container start), which restores build-once promotion; each repo
moves when convenient, and its config says which mode it uses. Anything in a
browser bundle is public, so secrets never belong in front-end build settings.

## D14 · Shared services publish versions; apps pin one

A service used by several apps is registered as a component. Dray builds it
when its repo gets a version tag, behind that repo's own CI gate, and keeps
one image per version; a component never deploys on its own. A consuming app
says `uses: <component>@<version>` instead of cloning and building it, and
Dray fills that version's digest into the app's own manifests. Upgrading is a
one-line change in a pull request, so shipping an app never upgrades a shared
service by accident. Dray shows which app runs which version and can open
upgrade pull requests. `dray config migrate` pins each app to the version it
runs today.

## D15 · Shared namespaces are allowed, with an ownership rule; new apps get their own

An app migrated from the JS tool keeps its namespace, which the registry
marks `shared`. Dray stamps every resource with its owning app, and a Kyverno
rule lets an app change only resources it owns. Requests and limits are
required so one app cannot starve its neighbours, and network rules select
pods per app. Every new app gets its own namespace, and existing apps move out
one at a time, stateless ones first.

## D16 · Deploy-on-merge is driven by Dray, off by default

Dray's GitHub App receives repository events. When CI finishes green on the
main branch, Dray plans and applies to every environment whose registry entry
sets `on_merge: deploy`; the finished run is the CI gate. For an environment
that needs approval, on-merge only opens the approval request. GitHub
workflows need no deploy step and hold no deploy credentials. Pilets can
publish when their version changes the same way.

Many repos deploy today by running the JS tool from GitHub Actions, each with
a cloud deploy role and decryption keys stored for the workflow. For the
cutover, the Go CLI keeps the JS tool's command names (`ship`, `push`,
`apply`, `rollout`, `status`, `rollback`, `publish`, `secrets sync`), so a
workflow switches by changing only its install step. Each repo then moves to
`on_merge` and its workflow drops the deploy role and keys.

## D17 · Third-party images are mirrored into the cluster's registry by digest

The registry lists approved upstream images. When a manifest names one by
tag, Dray resolves the tag to a digest, copies that image into the target's
container registry once, and rewrites the manifest to the mirrored digest;
the plan shows the rewrite. The policy stays absolute: every image that runs
was built or mirrored by Dray and is pinned by digest. Dray does the copy
itself, so no upstream registry credential is stored and the same flow works
on any cloud's registry.

## D18 · Builds that need another repo list it as a pinned source

Some builds use a second repo beside their own, such as a shared library the
projects reference by relative path, with a build context of the shared
parent folder. An image lists those repos in `sources`, each pinned to a
commit; Dray clones the app's repo into a folder named after its repository
and each source into its `path` next to it, so a context of `..` works
unchanged. Upgrading a source is a one-line change in a pull request.
`dray config migrate` finds sibling repos from the Dockerfile's COPY lines
and pins each to the local checkout's commit, for a person to confirm.
Publishing a shared library as a versioned package remains the long-term
direction, adopted per app when convenient.

## D19 · Where the CLI deploy differs from the JS tool's

The CLI deploy (M0.5) keeps every safety rule of the JS tool's `ship`, and
changes these on purpose:

- Images are built from the commit exported into a fresh folder, with each
  build source at its pinned commit beside it, not from the working tree and
  whatever happens to sit next to it. Uncommitted changes deploy only with
  `--allow-dirty`, and only to an environment whose registry entry sets
  `allow_dirty`.
- Manifests are applied server-side as field manager `dray`, and `kubectl
  diff` decides what changed. A workload that did not change is not applied
  or restarted; one whose config map, service or secret changed but whose
  pod template did not is restarted; one already rolling out is only waited
  on. The JS tool restarted every Deployment on every ship. `--restart`
  restarts anyway, for a secret changed by hand.
- A build argument env file that is missing, or holds no matching values, is
  an error. The JS tool skipped it, which builds a front end without its
  settings.
- Build argument values and registry passwords reach docker through its
  environment and stdin, never its command line.
- A per-environment image is tagged `<commit>-<env>`; a dirty build gets a
  tag of its own, since registries keep tags immutable.
- Dry runs ask the cluster what would change and report every problem a real
  deploy would stop on, instead of printing the commands they would run.

## D12 · Scope kept for later phases

Preview environments per pull request, release groups for services that ship
together, and an S3 + CloudFront target for static sites.
