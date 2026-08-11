const { test } = require('node:test'); const assert = require('node:assert/strict');
const { mergeDefaults, imageVar, resolveTargets } = require('../../src/core/resolve');
const registry = { sai: { path: '/x/sai', config: {
  name: 'sai',
  images: [{ name: 'sai-worker', ecr: 'sai-worker', source: { local: true } }],
  workloads: [{ name: 'sai-api', kind: 'deployment', image: 'sai-worker', manifests: ['.k8s/marketing/deployment.yaml'], dependsOn: ['secret:sai-secrets'] }],
  secrets: [{ name: 'sai-secrets', kind: 'sops-manifest', file: 's.enc.yaml' }],
} } };
const globalDefaults = { account: '123', region: 'us-east-2', context: 'st-eks', namespace: 'marketing' };
test('imageVar derives UPPER_SNAKE _IMAGE', () => { assert.equal(imageVar({ name: 'sai-worker' }), 'SAI_WORKER_IMAGE'); });
test('imageVar honors override', () => { assert.equal(imageVar({ name: 'x', templateVar: 'FOO' }), 'FOO'); });
test('mergeDefaults layers', () => { assert.equal(mergeDefaults({ region: 'a' }, { defaults: { region: 'b' } }, {}).region, 'b'); });
test('resolveTargets repo:name builds unit with stamp + repoUri + _secrets', () => {
  const [u] = resolveTargets({ registry, globalDefaults, spec: 'sai:sai-api' });
  assert.equal(u.workload, 'sai-api'); assert.equal(u.kind, 'deployment');
  assert.equal(u.repoUri, '123.dkr.ecr.us-east-2.amazonaws.com/sai-worker');
  assert.deepEqual(u.manifests, ['.k8s/marketing/deployment.yaml']);
  assert.deepEqual(u.stamp, [{ var: 'SAI_WORKER_IMAGE', repoUri: '123.dkr.ecr.us-east-2.amazonaws.com/sai-worker' }]);
  assert.deepEqual(u.dependsOn, ['secret:sai-secrets']);
  assert.deepEqual(u._secrets, [{ name: 'sai-secrets', kind: 'sops-manifest', file: 's.enc.yaml' }]);
});

const regWithDisabled = { sai: { path: '/x/sai', config: {
  name: 'sai',
  images: [
    { name: 'aws-cost', ecr: 'aws-cost', source: { local: true } },
    { name: 'chat-service', ecr: 'chat-service', source: { local: true } },
  ],
  workloads: [
    { name: 'aws-cost', kind: 'deployment', image: 'aws-cost', manifests: ['a'], disabled: true },
    { name: 'chat-service', kind: 'deployment', image: 'chat-service', manifests: ['b'] },
  ],
} } };
test('resolveTargets skips disabled workloads for bare repo', () => {
  const units = resolveTargets({ registry: regWithDisabled, globalDefaults, spec: 'sai' });
  assert.deepEqual(units.map((u) => u.workload), ['chat-service']);
});
test('explicitly targeting a disabled workload errors', () => {
  assert.throws(() => resolveTargets({ registry: regWithDisabled, globalDefaults, spec: 'sai:aws-cost' }), /disabled/);
});

const regNoImage = { sai: { path: '/x/sai', config: {
  name: 'sai',
  images: [],
  workloads: [{ name: 'searxng', kind: 'deployment', manifests: ['.k8s/searxng/deployment.yaml', '.k8s/searxng/configmap.yaml'] }],
} } };
test('workload with no image → apply-only unit (image null, empty stamp)', () => {
  const [u] = resolveTargets({ registry: regNoImage, globalDefaults, spec: 'sai:searxng' });
  assert.equal(u.image, null);
  assert.equal(u.repoUri, null);
  assert.deepEqual(u.stamp, []);
  assert.deepEqual(u.manifests, ['.k8s/searxng/deployment.yaml', '.k8s/searxng/configmap.yaml']);
});

// A `manual` workload is deployable on demand but must never be picked up by a
// bare-repo ship — that is what CI runs, so `manual` is how a repo keeps a
// workload out of its automatic pipeline without giving up `dray ship repo:name`.
const regWithManual = { sai: { path: '/x/sai', config: {
  name: 'sai',
  images: [
    { name: 'jobs-service', ecr: 'sai-jobs-service', source: { git: 'https://github.com/BrutalSystems/jobs-service' } },
    { name: 'chat-service', ecr: 'chat-service', source: { local: true } },
  ],
  workloads: [
    { name: 'jobs-service', kind: 'deployment', image: 'jobs-service', manifests: ['j'], manual: true, dependsOn: ['secret:jobs-secrets'] },
    { name: 'chat-service', kind: 'deployment', image: 'chat-service', manifests: ['b'] },
  ],
  secrets: [{ name: 'jobs-secrets', kind: 'sops-manifest', file: 'j.enc.yaml' }],
} } };
test('resolveTargets skips manual workloads for bare repo', () => {
  const units = resolveTargets({ registry: regWithManual, globalDefaults, spec: 'sai' });
  assert.deepEqual(units.map((u) => u.workload), ['chat-service']);
});
test('a manual workload does not leak its image back in as an image-only unit', () => {
  // Regression guard: the image-only fallback fires when no workload claims an
  // image. A skipped manual workload still counts as claiming it, so CI must not
  // build or push it.
  const units = resolveTargets({ registry: regWithManual, globalDefaults, spec: 'sai' });
  assert.deepEqual(units.map((u) => u.image && u.image.name), ['chat-service']);
});
test('--all skips manual workloads too', () => {
  const units = resolveTargets({ registry: regWithManual, globalDefaults, spec: '--all' });
  assert.deepEqual(units.map((u) => u.workload), ['chat-service']);
});
test('explicitly targeting a manual workload resolves it, unlike disabled', () => {
  const [u] = resolveTargets({ registry: regWithManual, globalDefaults, spec: 'sai:jobs-service' });
  assert.equal(u.workload, 'jobs-service');
  assert.equal(u.repoUri, '123.dkr.ecr.us-east-2.amazonaws.com/sai-jobs-service');
  assert.deepEqual(u.stamp, [{ var: 'JOBS_SERVICE_IMAGE', repoUri: '123.dkr.ecr.us-east-2.amazonaws.com/sai-jobs-service' }]);
  assert.deepEqual(u.dependsOn, ['secret:jobs-secrets']);
});
test('targeting a manual workload by its image name also resolves it', () => {
  const units = resolveTargets({ registry: regWithManual, globalDefaults, spec: 'sai:jobs-service' });
  assert.equal(units.length, 1);
});
