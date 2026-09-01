# dray

Convention-driven multi-repo deploy orchestrator — "forge, but for shipping."

`dray` builds/pushes container images to ECR, renders + applies k8s manifests
(pinning a git SHA into image references), rolls out workloads, publishes Piral
pilets, and syncs secrets — driven by a `.dray/config.json` committed in each
repo. One entry point, interactive or scripted; no daemon.

## Install

```bash
npm i -g @brutalsystems/dray
```

## Setup (per machine)

1. Global defaults — `~/.dray/config.json`:
   ```json
   {
     "defaults": {
       "profile": "<aws-profile>", "region": "<region>", "account": "<acct-id>",
       "platform": "linux/arm64", "context": "<kube-context>", "namespace": "<namespace>"
     }
   }
   ```
2. Register each repo: `dray add /path/to/repo` (reads its `.dray/config.json`;
   registry is stored at `~/.dray/registry.json`).

Requires `docker buildx`, `kubectl`, `git`, `aws`, and (for pilets/secrets) `sops`.

## Commands

```bash
dray                        # interactive menu
dray list                   # registered repos + targets
dray ship <repo>:<target>   # deps?→build→push(SHA)→render+apply→rollout
dray ship <repo>            # all enabled workloads in the repo (skips manual ones)
dray apply  <repo>:<target> # render + apply manifests only
dray rollout <repo>:<target>
dray status <repo>          # running image SHA vs HEAD
dray rollback <repo>:<target> <sha>
dray publish <repo>[:<pilet>]   # publish pilet(s) via sops exec-env
dray secrets sync <repo>
```

Global flags: `--dry-run` (print the plan, run nothing), `--allow-dirty`
(build a dirty tree, tags `:<sha>-dirty`), `--all` (every registered repo),
`--skip-ci-check` (deploy even if CI is red — see [CI gate](#ci-gate)).

## `.dray/config.json` (per repo)

```jsonc
{
  "name": "myrepo",
  // optional per-repo overrides of ~/.dray/config.json defaults
  // (profile, region, account, platform, context, namespace, ciGate);
  // a workload may override them again.
  "defaults": { "namespace": "bs", "ciGate": true },
  "images": [
    { "name": "app", "ecr": "app", "source": { "local": true },
      "dockerfile": "Dockerfile", "context": ".",
      // optional: rebuild a cached deps layer when lockfiles change
      "depsImage": { "dockerfile": "Dockerfile.deps", "tag": "app:deps", "rebuildOn": ["uv.lock"] },
      // optional: inject --build-arg from an env file (e.g. VITE_* from .env.production),
      // or from a sops-encrypted file decrypted in memory at build time (no plaintext on disk):
      //   "buildArgs": { "sopsEnvFile": "secrets.env", "prefix": "VITE_" }
      "buildArgs": { "envFile": ".env.production", "prefix": "VITE_" },
      // optional: also move a mutable <ecr>:latest tag onto every (non-dirty)
      // push, so workloads that reference <ecr>:latest track the newest build
      // without being re-shipped (e.g. rarely-shipped cronjobs sharing an image).
      "latest": true },
    // image from another repo (cloned to a tmp dir, built, pushed):
    { "name": "svc", "ecr": "svc", "source": { "git": "https://github.com/org/svc", "ref": "main" } }
  ],
  "workloads": [
    // kind: deployment (rolled out) or cronjob (applied only)
    { "name": "app", "kind": "deployment", "image": "app",
      "manifests": [".k8s/deployment.yaml", ".k8s/service.yaml"], "stampImages": ["app"] },
    // disabled: kept in config but skipped (e.g. served elsewhere).
    // Targeting it by name is an error.
    { "name": "legacy", "kind": "deployment", "image": "app", "manifests": ["..."], "disabled": true },
    // manual: deployed only by `dray ship <repo>:<name>`, never by a bare-repo
    // ship or --all. Use it to keep a workload out of CI (which ships the bare
    // repo) while still deploying it on demand. Its image is skipped too.
    { "name": "jobs", "kind": "deployment", "image": "jobs", "manifests": ["..."], "manual": true },
    // image-less: apply-only (third-party image lives literally in the manifest)
    { "name": "searxng", "kind": "deployment", "manifests": [".k8s/searxng.yaml"] }
  ],
  "secrets": [
    { "name": "app-secrets", "kind": "sops-manifest", "file": ".k8s/secrets.enc.yaml" }
  ],
  "pilets": {
    "secretsFile": "secrets.env",
    "command": ["npm", "run", "publish:feed", "--", "--pilet", "{name}"],
    "names": ["foo", "bar"]
  }
}
```

### CI gate

With `"ciGate": true` in a repo's `defaults`, `push`, `apply`, `rollout` and
`ship` refuse to run unless that repo's GitHub Actions CI is green for the code
being shipped. It reads `gh run list` (so `gh` must be installed and
authenticated) and blocks on all three of:

- **failed** — CI is red.
- **pending** — CI is still running. This is the case that motivated the gate:
  a deploy once pushed an image to ECR six minutes before the test suite it was
  racing had finished, and nothing would have stopped a red result.
- **absent** — no run exists for HEAD *or any recent ancestor*, i.e. CI has
  never run for this line of work. "No run found" has to block: if it passed,
  never pushing would be the way around the gate.

It does *not* demand a run for HEAD itself. Workflows commonly use
`paths-ignore`, so a docs-only commit produces no run at all; requiring one
would refuse to ship a README change, and a gate that blocks legitimate work
gets switched off. Instead dray walks back to the **newest CI-covered
ancestor** and requires that verdict to be green.

Default is off, so registering a repo does not change its behavior — arm it
per repo. `--skip-ci-check` overrides the gate for one command and prints a
loud warning. The gate also self-disables when `GITHUB_ACTIONS=true` (inside
CI it would be waiting on the run that invoked it — a deadlock) and on
`--dry-run`.

**What it does not cover**, deliberately:

- Images built from `source.git` — their code lives in another repository whose
  CI is not this repo's to read, so those units are skipped rather than falsely
  reported as verified.
- `rollback`, which re-applies a SHA that was already deployed. Blocking a
  rollback because `main` is red would be exactly backwards during an incident.
- `publish` (pilets), which does not go through the deploy path.
- A commit you have not pushed: with no run of its own it falls back to its
  newest covered ancestor, so an unpushed change on top of a green commit
  passes. The gate closes the red/racing cases, not "I never pushed it".

It reads the repo's 100 most recent workflow runs. In a repo busy enough that
HEAD's run has already fallen out of that window, the gate reports `absent` and
blocks — noisy, but never the wrong way around.

### Image pinning

Manifests reference managed images by a placeholder var — `${APP_IMAGE}`
(derived `<UPPER_SNAKE(name)>_IMAGE`, or set `templateVar`). On apply, dray
substitutes `<ecr>:<gitSHA>` for that var across the listed files (container
image, env-var image refs, cronjob images — uniformly). A raw `kubectl apply`
of an unrendered manifest fails loud, so applies always go through dray.

An image with `"latest": true` additionally gets a mutable `<ecr>:latest` tag
moved onto each non-dirty push. This is for the opposite need: a workload that
should track the newest build of a shared image *without* being re-shipped
(e.g. a low-frequency cronjob sharing an image with a frequently-shipped
service). Reference `<ecr>:latest` literally in that manifest (with
`imagePullPolicy: Always`) instead of the `${..._IMAGE}` placeholder. You trade
away per-commit reproducibility/rollback for that workload — use it only where
that's the point.
