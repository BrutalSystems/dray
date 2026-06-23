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
dray ship <repo>            # all enabled workloads in the repo
dray apply  <repo>:<target> # render + apply manifests only
dray rollout <repo>:<target>
dray status <repo>          # running image SHA vs HEAD
dray rollback <repo>:<target> <sha>
dray publish <repo>[:<pilet>]   # publish pilet(s) via sops exec-env
dray secrets sync <repo>
```

Global flags: `--dry-run` (print the plan, run nothing), `--allow-dirty`
(build a dirty tree, tags `:<sha>-dirty`), `--all` (every registered repo).

## `.dray/config.json` (per repo)

```jsonc
{
  "name": "myrepo",
  "images": [
    { "name": "app", "ecr": "app", "source": { "local": true },
      "dockerfile": "Dockerfile", "context": ".",
      // optional: rebuild a cached deps layer when lockfiles change
      "depsImage": { "dockerfile": "Dockerfile.deps", "tag": "app:deps", "rebuildOn": ["uv.lock"] },
      // optional: inject --build-arg from an env file (e.g. VITE_* from .env.production)
      "buildArgs": { "envFile": ".env.production", "prefix": "VITE_" } },
    // image from another repo (cloned to a tmp dir, built, pushed):
    { "name": "svc", "ecr": "svc", "source": { "git": "https://github.com/org/svc", "ref": "main" } }
  ],
  "workloads": [
    // kind: deployment (rolled out) or cronjob (applied only)
    { "name": "app", "kind": "deployment", "image": "app",
      "manifests": [".k8s/deployment.yaml", ".k8s/service.yaml"], "stampImages": ["app"] },
    // disabled: kept in config but skipped (e.g. served elsewhere)
    { "name": "legacy", "kind": "deployment", "image": "app", "manifests": ["..."], "disabled": true },
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

### Image pinning

Manifests reference managed images by a placeholder var — `${APP_IMAGE}`
(derived `<UPPER_SNAKE(name)>_IMAGE`, or set `templateVar`). On apply, dray
substitutes `<ecr>:<gitSHA>` for that var across the listed files (container
image, env-var image refs, cronjob images — uniformly). A raw `kubectl apply`
of an unrendered manifest fails loud, so applies always go through dray.
