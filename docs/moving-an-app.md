# Moving an app to Dray

How to move an app that deploys with the JS dray (or by hand) to Dray v2,
ending with deploy on merge. Each step can be checked before the next, and
nothing changes in the cluster until step 4.

You need: the `dray` CLI; access to the app's cloud account, cluster and
secret store; and a running `dray-server` with a GitHub App for the app's
organization (one App per organization, because a private App can only be
installed in the organization that owns it).

## 1. Convert the config

```bash
dray config migrate            # prints .dray/config.yaml and what to check
dray config migrate --write
```

Read every warning, then edit `.dray/config.yaml`:

- **Environments.** One per place the app runs: `target`, `namespace`,
  `release: direct`, `approval: none` (deploy on merge needs it for now). An
  app with its own namespace is `owned` (the default); keep `shared` only for
  a namespace other apps also use.
- **Targets and the secret store.** The cluster and cloud account, and where
  `secret:` references live (for OpenBao with a `{client}` layout, set
  `client`). Credentials never go here; your own AWS profile goes in
  `~/.config/dray/config.yaml` under `aws_profiles`.
- **Namespace manifests.** Remove any `Namespace` object from the workloads'
  manifests. The server may not change namespaces; a person creates and
  labels them (step 3).
- **Secrets.** List under each workload the secrets it needs (`secrets:`), so
  Dray creates them from the store before applying it.
- **Shared components.** Pin each `uses` entry to the version or commit
  running today, with the `repository` its images are in.
- **Front-end build settings.** Values a CI job used to write into a file
  (such as Vite settings) are public once built; put them in
  `build_args.values` so the build doesn't depend on CI. Never put secrets in
  build arguments.
- **Anything applied by hand.** Look in the namespace for objects that aren't
  in the repo (disruption budgets, network policies, config maps) and add
  them to the manifests. Moving namespaces leaves hand-applied objects
  behind.

`dray config validate` checks the result.

## 2. Move the secrets to the store

Check first that the repo's secret files still match what the cluster runs:
compare each key's fingerprint (a hash of the value), never the values.
Secrets synced by hand drift. Then:

```bash
dray secrets import . --env prod            # dry run: key names and destinations only
dray secrets import . --env prod --write
```

Nothing prints a value. A secret the store already holds with other values is
kept unless you pass `--overwrite`.

## 3. Give the server access

The server deploys with its own identity, and only where it is allowed:

- **Secret store.** A role for the server's service account that reads only
  this app's folder for the environment (with OpenBao, a Kubernetes auth
  role; Dray signs in as `kv-{client}-{app}-{env}` by default).
- **Kubernetes.** Grant `deploy/kubernetes/deployer.yaml` in the app's
  namespace:
  `sed 's/APP_NAMESPACE/<namespace>/' deploy/kubernetes/deployer.yaml | kubectl apply -f -`
- **Namespace.** It must exist. For an owned namespace, label it
  `dray/app=<app name>`; Dray then refuses any other app there.

## 4. Try it from the CLI

From a clean checkout (uncommitted changes in the app's folder stop a deploy):

```bash
dray deploy --dry-run
```

It reports every problem a deploy would stop on and shows what would change
in the cluster. When the app's current code is already running, only image
references should change. A manual `dray deploy` is the first real deploy if
you want one before CI takes over.

## 5. CI that deploys nothing

Add a workflow that builds and tests the app on pushes to the main branch and
on pull requests. It holds no cloud role and no secrets. Remove the old
deploy workflow. Dray deploys a commit only when every workflow run for it
has passed, so a commit with no CI never deploys.

## 6. Turn on deploy on merge

- Set `on_merge: deploy` on the environment.
- Install the organization's App on this repo only.
- Merge. When CI passes on the merge commit, Dray builds the images in the
  cluster, deploys them, and posts a `dray: <app> → <env>` check on the
  commit with the log.

## 7. Check

- The check on the commit: success, and its log.
- The workloads run the new image digests, and the site answers.
- The App's webhook deliveries: `workflow_run.completed` accepted (202).

## Things learned moving apps

- Objects applied by hand outside the repo get lost when an app moves
  namespace; put them in the repo.
- A repo with apps in subfolders works: each folder with `.dray/config.yaml`
  is an app, and only changes inside it count as uncommitted.
- The first deploy rebuilds every image for the new commit; later deploys
  reuse the registry's build cache.
- A cloud deploy profile scoped to some repositories can't push to others;
  `dray deploy --aws-profile` overrides it for one deploy.
