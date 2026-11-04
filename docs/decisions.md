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

Core depends on a `Store` interface. Repos use logical
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

An app migrated from the JS tool keeps its namespace, which its config
marks `shared`. Dray stamps every resource with its owning app, and a Kyverno
rule lets an app change only resources it owns. Requests and limits are
required so one app cannot starve its neighbours, and network rules select
pods per app. Every new app gets its own namespace, and existing apps move out
one at a time, stateless ones first.

## D16 · Deploy-on-merge is driven by Dray, off by default

Dray's GitHub App receives repository events. When CI finishes green on the
main branch, Dray plans and applies to every environment whose config sets
`on_merge: deploy`; the finished run is the CI gate. For an environment
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

The app's config lists approved upstream images (`third_party`). When a manifest names one by
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
  `--allow-dirty`, and only to an environment whose config sets
  `allow_dirty`. A context of `..` is that build folder, holding the repo's
  checkout (named after its repository) and its sources, if any; it needs no
  sources of its own.
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

## D20 · No separate registry: an app's repo holds everything it needs to deploy

This reopens the registry, the per-app file outside the repo that said where
an app may deploy and who approves. Everything about an app is now in its
repo's `.dray/config.yaml`: environments, the targets they name, the secret
store, and third-party images. Anyone who clones the repo and has Dray and the
access can deploy it. The registry was there so a change in the repo could not
send the app somewhere else; that is now kept by:

- Dray reads where an app may deploy from the main branch only, never from the
  branch being deployed.
- Changes to `.dray/` need the owner's review (CODEOWNERS on a protected main).
- Dray's own settings can require approval for an environment; a repo can ask
  for more, never less.
- One owner per namespace: Dray labels a namespace it creates or first deploys
  to with the app (`dray/app`), and refuses any other app there. A namespace
  marked `shared` is not claimed.

Credentials are never in a repo. A person's own settings
(`~/.config/dray/config.yaml`, optional) map an account to their AWS profile
and a cluster to their kube context. The list of apps is the repos with a
`.dray/config.yaml`. Shared components keep their pin in the app's `uses`,
with the repository their images live in.

## D21 · Dray publishes pilets itself; commit versions for dev feeds

`dray publish` builds each pilet with the repo's own build, packs it with the
pilet CLI, rewrites the version inside the package, and uploads it to the
feed of a `release: feed` environment with the key from the secret store. A
feed environment's `version: commit` (the default) publishes `1.2.0` as
`1.2.0-<env>.<commit>`, so every commit is a distinct, immutable version and
nobody bumps package.json for a dev publish; `version: package` publishes the
package.json version for production feeds. The repo's own publish command
stays available as `pilets.command`, the JS tool's behavior. Feeds that never
serve prerelease versions unless pinned need a pin or `version: package`.

Uncommitted changes count only inside the app's own folder, so an app in a
subfolder of a monorepo is not blocked by changes elsewhere in the repo.

## D22 · Deploy on merge first, before the rest of the server

The first server feature is deploy on merge, the smallest piece that removes
deploy credentials from people and CI. It is a GitHub App webhook receiver
that runs the CLI's deploy code with the server's own identity. The trigger
is `workflow_run` completed for a push to the default branch, and a commit
deploys only when every workflow run for it has passed. Only environments
with `release: direct` and `approval: none` deploy on merge until approvals
exist; others get a neutral check saying so. Commits arrive as GitHub
tarballs, not clones. There is no database yet: results live in GitHub check
runs and the server's log, and a restart forgets which commits it handled
(the next CI webhook re-checks).

## D23 · Builds run in a pod per deploy, on nodes that exist only while building

The server does not keep a builder running. For a deploy that builds, it
starts one rootless BuildKit pod, builds every image of the deploy on it,
and deletes it. The pod requests all the memory it may use (memory request
equals its limit), so the scheduler and Karpenter reserve it, and it runs
only on a `dray-build` NodePool whose nodes carry a taint nothing else
tolerates. Karpenter starts a build node (spot first, on-demand when there
is none) when a build pod cannot be placed and removes it a minute after it
is empty. Builds and other workloads never share a node, so a large build
cannot squeeze an app, and no node waits for builds; the price is one or two
minutes for a node to start. A fresh BuildKit has no layer cache, so each
image's cache is kept in the registry (`dray-cache/<repo>`, mutable tags,
while image repositories stay immutable). A pod a crashed server leaves
behind ends after two hours, and its node with it.

## D12 · Scope kept for later phases

Preview environments per pull request, release groups for services that ship
together, and an S3 + CloudFront target for static sites.
