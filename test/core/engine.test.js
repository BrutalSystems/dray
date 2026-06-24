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
