const { test } = require('node:test'); const assert = require('node:assert/strict');
const { execute } = require('../../src/core/engine');
function deps(log) {
  return {
    git: { currentSha: async () => 'abc', isDirty: async () => false, cloneToTmp: async () => ({ dir: '/clone', sha: 'gitsha', cleanup: () => log.push('cleanup') }) },
    depsCache: { needsDepsRebuild: () => false },
    docker: { buildDeps: async () => log.push('deps'), buildImage: async ({ sha }) => log.push(`build:${sha}`), pushImage: async ({ sha }) => log.push(`push:${sha}`), ecrLogin: async () => log.push('login'), ensureRepo: async () => log.push('ensure') },
    kubectl: { applyFile: async ({ file }) => log.push(`apply:${file}`), rollout: async ({ deployment }) => log.push(`roll:${deployment}`), rolloutUndo: async () => log.push('undo') },
    secrets: { syncSecret: async (s) => log.push(`secret:${s.name}`) },
    render: { renderManifests: (files, vars) => { log.push(`render:${JSON.stringify(vars)}`); return files.map((f) => `/r/${f}`); } },
  };
}
test('build+push+apply+rollout with computed sha and rendered vars', async () => {
  const log = [];
  const unit = { repo: 'sai', repoPath: '/x', image: { name: 'agent', source: { local: true } }, workload: 'agent', kind: 'deployment',
    manifests: ['d.yaml'], dependsOn: [], defaults: { context: 'c', namespace: 'n', platform: 'p', account: 'a', region: 'r' },
    repoUri: 'r/agent', stamp: [{ var: 'AGENT_IMAGE', repoUri: 'r/agent' }] };
  const steps = [{ kind: 'build', unit }, { kind: 'push', unit }, { kind: 'apply', unit }, { kind: 'rollout', unit }];
  await execute(steps, { deps: deps(log) });
  assert.deepEqual(log, ['build:abc', 'login', 'ensure', 'push:abc', 'render:{"AGENT_IMAGE":"r/agent:abc"}', 'apply:/r/d.yaml', 'roll:agent']);
});
test('push step forwards image.latest so the push maintains a :latest tag', async () => {
  const log = []; const d = deps(log);
  let receivedLatest;
  d.docker.pushImage = async ({ sha, latest }) => { receivedLatest = latest; log.push(`push:${sha}`); };
  const unit = { repo: 'sai', repoPath: '/x', image: { name: 'sai-worker', ecr: 'sai-worker', source: { local: true }, latest: true },
    workload: 'llmcatalog-discover', kind: 'cronjob', manifests: [], dependsOn: [],
    defaults: { context: 'c', namespace: 'n', platform: 'p', account: 'a', region: 'r' }, repoUri: 'r/sai-worker', stamp: [] };
  await execute([{ kind: 'push', unit }], { deps: d });
  assert.equal(receivedLatest, true);
});
test('secret step syncs the carried secret object with the unit namespace', async () => {
  const log = []; const d = deps(log);
  const unit = { repo: 'sai', repoPath: '/x', image: { name: 'a', source: { local: true } },
    workload: 'sai-api', kind: 'deployment', manifests: [], dependsOn: ['secret:sai-secrets'],
    defaults: { context: 'st-eks', namespace: 'marketing', profile: 'st' }, stamp: [] };
  const step = { kind: 'secret', secret: { name: 'sai-secrets', kind: 'sops-manifest', file: 's.enc.yaml' }, unit };
  d.secrets.syncSecret = async (s, ctx) => log.push(`secret:${s.name}:${s.file}:${ctx.namespace}`);
  await execute([step], { deps: d });
  assert.deepEqual(log, ['secret:sai-secrets:s.enc.yaml:marketing']);
});
test('build awaits resolveBuildArgs and forwards the resolved args to buildImage', async () => {
  const log = []; const d = deps(log);
  let received;
  d.docker.buildImage = async ({ sha, buildArgs }) => { received = buildArgs; log.push(`build:${sha}`); };
  d.buildArgs = { resolveBuildArgs: async () => ['VITE_X=1'] };
  const unit = { repo: 'sai', repoPath: '/x', image: { name: 'app', source: { local: true } }, defaults: {}, stamp: [] };
  await execute([{ kind: 'build', unit }], { deps: d });
  assert.deepEqual(received, ['VITE_X=1']);
});
test('dirty local tree throws unless allowDirty', async () => {
  const d = deps([]); d.git.isDirty = async () => true;
  const unit = { repo: 'sai', repoPath: '/x', image: { name: 'a', source: { local: true } }, defaults: {}, stamp: [] };
  await assert.rejects(() => execute([{ kind: 'build', unit }], { deps: d }), /dirty/);
});
test('dry-run of a git-source image shows the git source as cwd, not the local repo', async () => {
  const log = []; const d = deps(log);
  let cwd;
  d.docker.buildImage = async (a) => { cwd = a.cwd; log.push(`build:${a.sha}`); };
  d.buildArgs = { resolveBuildArgs: async () => [] };
  const unit = { repo: 'ems-be', repoPath: '/local/ems-be',
    image: { name: 'jobs-service', source: { git: 'git@github.com:Org/jobs-service.git', ref: 'main' } },
    defaults: {}, stamp: [] };
  await execute([{ kind: 'build', unit }], { dryRun: true, deps: d });
  assert.equal(log[0], 'build:DRYRUN');
  assert.equal(cwd, '<git git@github.com:Org/jobs-service.git@main — cloned at build>');
  assert.notEqual(cwd, '/local/ems-be');
});

// --- ship must not roll the deployment twice --------------------------------
//
// `apply` stamps the manifest with the new image SHA, which the Deployment
// controller acts on immediately. The rollout step that follows was then
// issuing `rollout restart` regardless, producing a second ReplicaSet and a
// second pod replacement. Observed on cxx-mcp: two ReplicaSets one second
// apart on every ship, and a deployment revision count in the high 80s.
function shipSteps(unit) {
  return [{ kind: 'apply', unit }, { kind: 'rollout', unit }];
}
function shipUnit() {
  return { repo: 'r', repoPath: '/x', image: { name: 'agent', source: { local: true } }, workload: 'agent',
    kind: 'deployment', manifests: ['d.yaml'], dependsOn: [],
    defaults: { context: 'c', namespace: 'n', platform: 'p', account: 'a', region: 'r' },
    repoUri: 'r/agent', stamp: [{ var: 'AGENT_IMAGE', repoUri: 'r/agent' }] };
}

test('ship does not restart when a rollout is already under way', async () => {
  const log = []; const d = deps(log);
  let sawRestart;
  d.kubectl.rolloutInProgress = async () => true;
  d.kubectl.rollout = async ({ restart }) => { sawRestart = restart; log.push('roll'); };
  await execute(shipSteps(shipUnit()), { deps: d });
  assert.equal(sawRestart, false, 'apply already triggered the rollout; restart would be a second one');
});

test('ship still restarts when the deployment is stable', async () => {
  // Re-shipping the same commit to pick up a changed Secret: apply is a no-op, so nothing
  // is in flight and the restart is the only thing that cycles pods. Removing it outright
  // would silently do nothing here.
  const log = []; const d = deps(log);
  let sawRestart;
  d.kubectl.rolloutInProgress = async () => false;
  d.kubectl.rollout = async ({ restart }) => { sawRestart = restart; log.push('roll'); };
  await execute(shipSteps(shipUnit()), { deps: d });
  assert.equal(sawRestart, true);
});

test('ship restarts when rollout state cannot be determined', async () => {
  // Conservative: an unreadable/absent deployment must not silently skip the restart.
  const log = []; const d = deps(log);
  let sawRestart;
  d.kubectl.rolloutInProgress = async () => { throw new Error('kubectl exploded'); };
  d.kubectl.rollout = async ({ restart }) => { sawRestart = restart; log.push('roll'); };
  await execute(shipSteps(shipUnit()), { deps: d });
  assert.equal(sawRestart, true);
});

test('dry-run does not query the cluster and shows the restart', async () => {
  const log = []; const d = deps(log);
  let queried = false; let sawRestart;
  d.kubectl.rolloutInProgress = async () => { queried = true; return true; };
  d.kubectl.rollout = async ({ restart }) => { sawRestart = restart; log.push('roll'); };
  await execute(shipSteps(shipUnit()), { deps: d, dryRun: true });
  assert.equal(queried, false, 'a dry run must not touch the cluster');
  assert.equal(sawRestart, true);
});

test('a rollout-only invocation still skips the restart (regression: CI double-roll)', async () => {
  // THE BUG THIS FIXES. CI runs `dray apply` and `dray rollout` as two separate processes,
  // so the apply step cannot hand anything to the rollout step. Observed 2026-09-01 in
  // brokenhip-be: two ReplicaSets three seconds apart, every pod replaced twice per deploy.
  // The rollout step must reach its own conclusion from cluster state alone.
  const log = []; const d = deps(log);
  let sawRestart;
  d.kubectl.rolloutInProgress = async () => true;
  d.kubectl.rollout = async ({ restart }) => { sawRestart = restart; log.push('roll'); };
  const u = shipUnit();
  await execute([{ kind: 'rollout', label: 'rollout agent', unit: u }], { deps: d });
  assert.equal(sawRestart, false, 'no apply ran in THIS process; the cluster is the only source of truth');
});

test('cronjobs never query rollout state', async () => {
  const log = []; const d = deps(log);
  let queried = false;
  d.kubectl.rolloutInProgress = async () => { queried = true; return true; };
  d.kubectl.rollout = async () => { log.push('roll'); };
  const u = shipUnit(); u.kind = 'cronjob';
  await execute([{ kind: 'rollout', label: 'rollout agent', unit: u }], { deps: d });
  assert.equal(queried, false, 'cronjobs have no rollout to be in flight');
});

// ── Rollout timeout is not rollout failure ───────────────────────────────────
function _rolloutUnit() {
  return { repo: 'sai', repoPath: '/x', image: { name: 'api', source: { local: true } }, workload: 'api',
    kind: 'deployment', manifests: [], dependsOn: [],
    defaults: { context: 'c', namespace: 'n', platform: 'p', account: 'a', region: 'r' },
    repoUri: 'r/api', stamp: [] };
}

test('a slow rollout is NOT rolled back when Kubernetes still considers it healthy', async () => {
  // `kubectl rollout status --timeout` exiting non-zero means "I stopped
  // waiting", not "this is broken". Undoing on that reverts working code and
  // doubles the pod churn -- observed twice on st-eks, where a cold node
  // pulling a 1.1GB image blew the old 180s wait while the new pod was 1/1.
  const log = []; const d = deps(log);
  d.kubectl.rollout = async () => { throw new Error('timed out waiting for the condition'); };
  d.kubectl.rolloutFailed = async () => false;      // cluster: still progressing
  d.kubectl.rolloutUndo = async () => log.push('undo');
  await execute([{ kind: 'rollout', unit: _rolloutUnit() }], { deps: d });
  assert.ok(!log.includes('undo'), `rolled back a healthy deploy: ${log}`);
});

test('a genuinely failed rollout IS rolled back', async () => {
  const log = []; const d = deps(log);
  d.kubectl.rollout = async () => { throw new Error('deadline exceeded'); };
  d.kubectl.rolloutFailed = async () => true;       // cluster: ProgressDeadlineExceeded
  d.kubectl.rolloutUndo = async () => log.push('undo');
  await assert.rejects(() => execute([{ kind: 'rollout', unit: _rolloutUnit() }], { deps: d }));
  assert.ok(log.includes('undo'), `did not roll back a real failure: ${log}`);
});

test('an unreadable deployment does not trigger a rollback', async () => {
  // Conservative in the direction that does no harm: a missed rollback leaves
  // running code running; a wrong one takes down something that was fine.
  const log = []; const d = deps(log);
  d.kubectl.rollout = async () => { throw new Error('timed out'); };
  d.kubectl.rolloutFailed = async () => { throw new Error('kubectl unreachable'); };
  d.kubectl.rolloutUndo = async () => log.push('undo');
  await execute([{ kind: 'rollout', unit: _rolloutUnit() }], { deps: d });
  assert.ok(!log.includes('undo'), `rolled back on an unreadable deployment: ${log}`);
});
