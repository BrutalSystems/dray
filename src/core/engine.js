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
        // Read the running image BEFORE applying. If it differs from what we
        // are about to stamp, this apply changes the pod template and the
        // Deployment controller starts a rollout on its own — so the rollout
        // step that follows must wait rather than restart, or every ship
        // produces two ReplicaSets and replaces the pod twice.
        //
        // Deliberately conservative: any doubt (dry run, cronjob, unreadable
        // deployment) leaves the restart in place, which is the previous
        // behaviour.
        u._applyStartsRollout = false;
        if (!dryRun && u.workload && u.kind !== 'cronjob') {
          try {
            const before = await d.kubectl.runningImage({ workload: u.workload, kind: u.kind, context: u.defaults.context, namespace: u.defaults.namespace });
            u._applyStartsRollout = before !== `${u.repoUri}:${sha}`;
          } catch { u._applyStartsRollout = false; }
        }
        for (const f of files) await d.kubectl.applyFile({ file: f, context: u.defaults.context, namespace: u.defaults.namespace, dryRun });
      } else if (step.kind === 'rollout') {
        try { await d.kubectl.rollout({ deployment: u.workload, context: u.defaults.context, namespace: u.defaults.namespace, dryRun, restart: !u._applyStartsRollout }); }
        catch (err) { await d.kubectl.rolloutUndo({ deployment: u.workload, context: u.defaults.context, namespace: u.defaults.namespace, dryRun }); throw err; }
      }
    }
  } finally {
    for (const c of clones) c.cleanup();
    for (const dir of renderDirs) fs.rmSync(dir, { recursive: true, force: true });
  }
}
module.exports = { execute };
