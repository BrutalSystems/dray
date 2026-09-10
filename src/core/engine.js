const fs = require('node:fs'); const path = require('node:path');
const real = {
  docker: require('../primitives/docker'), kubectl: require('../primitives/kubectl'),
  git: require('../primitives/git'), secrets: require('../primitives/secrets'),
  render: require('./render'), depsCache: require('./depsCache'),
  buildArgs: require('./buildArgs'),
};
async function shaForUnit(u, d, cache, dryRun, allowDirty) {
  if (!u.image) return null; // apply-only workload: no image, no sha/build
  const key = `${u.repo}/${u.image.name}`;
  if (cache.has(key)) return cache.get(key);
  let sha;
  if (u.image.source && u.image.source.git) {
    if (dryRun) sha = 'DRYRUN';
    else { const c = await d.git.cloneToTmp(u.image.source); u._clone = c; sha = c.sha; }
  } else if (dryRun) {
    sha = 'DRYRUN';
  } else {
    const dirty = await d.git.isDirty(u.repoPath);
    if (dirty && !allowDirty) throw new Error(`working tree is dirty in ${u.repoPath} (commit, or pass --allow-dirty)`);
    sha = await d.git.currentSha(u.repoPath);
    if (dirty) sha = `${sha}-dirty`;
  }
  cache.set(key, sha);
  return sha;
}
async function execute(steps, { dryRun = false, allowDirty = false, deps } = {}) {
  const d = { ...real, ...deps };
  const shaCache = new Map(); const clones = []; const renderDirs = [];
  let loggedIn = false;
  try {
    for (const step of steps) {
      const u = step.unit;
      if (step.kind === 'secret') {
        await d.secrets.syncSecret(step.secret, { repoPath: u.repoPath, context: u.defaults.context, namespace: u.defaults.namespace, profile: u.defaults.profile, dryRun });
        continue;
      }
      const sha = await shaForUnit(u, d, shaCache, dryRun, allowDirty);
      if (u._clone && !clones.includes(u._clone)) clones.push(u._clone);
      // Real builds use the clone dir. On dry-run we skip the clone, so show the
      // git source (not the local repo `dray` was invoked from) — otherwise the
      // printed command misleadingly implies the local Dockerfile is built.
      const src = u.image && u.image.source;
      const cwd = u._clone ? u._clone.dir
        : (dryRun && src && src.git ? `<git ${src.git}@${src.ref || 'HEAD'} — cloned at build>` : u.repoPath);
      if (step.kind === 'deps') { if (d.depsCache.needsDepsRebuild(u.image, cwd, u.repo)) await d.docker.buildDeps(u.image, cwd, dryRun); }
      else if (step.kind === 'build') await d.docker.buildImage({ ecrUri: u.repoUri, sha, dockerfile: u.image.dockerfile, context: u.image.context || '.', platform: u.defaults.platform, cwd, dryRun, buildArgs: await d.buildArgs.resolveBuildArgs(u.image, cwd, { dryRun }) });
      else if (step.kind === 'push') {
        if (!loggedIn) { await d.docker.ecrLogin({ account: u.defaults.account, region: u.defaults.region, profile: u.defaults.profile, dryRun }); await d.docker.ensureRepo({ ecr: u.image.ecr, account: u.defaults.account, region: u.defaults.region, profile: u.defaults.profile, dryRun }); loggedIn = true; }
        await d.docker.pushImage({ ecrUri: u.repoUri, sha, latest: !!u.image.latest, dryRun });
      } else if (step.kind === 'apply') {
        const vars = {}; for (const s of u.stamp) vars[s.var] = `${s.repoUri}:${sha}`;
        const files = d.render.renderManifests(u.manifests, vars, u.repoPath, { dryRun });
        if (files[0]) renderDirs.push(path.dirname(files[0]));
        // Applying may or may not change the pod template; the rollout step below decides
        // whether a restart is needed by asking the CLUSTER, not by us telling it from here.
        // A flag set in this step cannot reach a separate `dray rollout` invocation.
        for (const f of files) await d.kubectl.applyFile({ file: f, context: u.defaults.context, namespace: u.defaults.namespace, dryRun });
      } else if (step.kind === 'rollout') {
        // If the controller is already rolling this deployment -- because an apply just
        // changed the pod template, in THIS process or a previous `dray apply` invocation --
        // then `rollout restart` would start a second one, producing a second ReplicaSet and
        // replacing every pod twice. Wait for the one in flight instead.
        //
        // Conservative on doubt (dry run, cronjob, unreadable deployment): keep restarting,
        // because a skipped restart silently does nothing at all.
        let inFlight = false;
        if (!dryRun && u.kind !== 'cronjob') {
          try {
            inFlight = await d.kubectl.rolloutInProgress({ deployment: u.workload, context: u.defaults.context, namespace: u.defaults.namespace });
          } catch { inFlight = false; }
        }
        // A rollout error is NOT automatically a rollout failure.
        //
        // `kubectl rollout status --timeout` exiting non-zero means "I stopped
        // waiting". Rolling back on that reverts working code and replaces every
        // pod a second time -- observed twice on st-eks, where a cold node
        // pulling a 1.1GB image blew the wait while the new pod was already 1/1.
        //
        // So ask the cluster. ProgressDeadlineExceeded is Kubernetes giving up,
        // and that is the only thing worth undoing for. Anything else -- still
        // progressing, or unreadable -- leaves the deploy alone: a missed
        // rollback leaves running code running, a wrong one takes down
        // something that was fine.
        try {
          await d.kubectl.rollout({ deployment: u.workload, context: u.defaults.context, namespace: u.defaults.namespace, dryRun, restart: !inFlight, timeoutSeconds: u.rolloutTimeoutSeconds });
        } catch (err) {
          let failed = false;
          try { failed = await d.kubectl.rolloutFailed({ deployment: u.workload, context: u.defaults.context, namespace: u.defaults.namespace }); }
          catch { failed = false; }
          if (failed) {
            await d.kubectl.rolloutUndo({ deployment: u.workload, context: u.defaults.context, namespace: u.defaults.namespace, dryRun });
            throw err;
          }
          console.warn(`[dray] ${u.workload}: stopped waiting, but the cluster still reports the rollout progressing — leaving it alone. Check: kubectl rollout status deployment/${u.workload} -n ${u.defaults.namespace} --context ${u.defaults.context}`);
        }
      }
    }
  } finally {
    for (const c of clones) c.cleanup();
    for (const dir of renderDirs) fs.rmSync(dir, { recursive: true, force: true });
  }
}
module.exports = { execute };
