const { test } = require('node:test');
const assert = require('node:assert/strict');
const { validateRepoConfig } = require('../../src/config/repo');
const valid = {
  name: 'sai',
  images: [{ name: 'sai-worker', ecr: 'sai-worker', source: { local: true } }],
  workloads: [{ name: 'sai-api', kind: 'deployment', image: 'sai-worker', manifests: ['.k8s/marketing/deployment.yaml'] }],
  secrets: [{ name: 'sai-secrets', kind: 'sops-manifest', file: '.k8s/marketing/sai-secrets.enc.yaml' }],
};
test('accepts a valid config', () => { assert.equal(validateRepoConfig(valid).name, 'sai'); });
test('accepts a workload with no image (apply-only)', () => {
  const cfg = { name: 'sai', images: [], workloads: [{ name: 'searxng', kind: 'deployment', manifests: ['a'] }] };
  assert.equal(validateRepoConfig(cfg).name, 'sai');
});
test('rejects workload referencing unknown image', () => {
  const bad = { ...valid, workloads: [{ name: 'x', kind: 'deployment', image: 'nope', manifests: ['a'] }] };
  assert.throws(() => validateRepoConfig(bad), /unknown image "nope"/);
});
test('rejects workload with empty manifests', () => {
  const bad = { ...valid, workloads: [{ name: 'x', kind: 'deployment', image: 'sai-worker', manifests: [] }] };
  assert.throws(() => validateRepoConfig(bad), /manifests/);
});
test('rejects bad workload kind', () => {
  const bad = { ...valid, workloads: [{ name: 'x', kind: 'job', image: 'sai-worker', manifests: ['a'] }] };
  assert.throws(() => validateRepoConfig(bad), /kind/);
});
test('rejects bad secret kind', () => {
  const bad = { ...valid, secrets: [{ name: 's', kind: 'plain' }] };
  assert.throws(() => validateRepoConfig(bad), /secret .* kind/);
});
test('rejects non-boolean manual (a JSON string is truthy and would silently skip CI)', () => {
  const bad = { ...valid, workloads: [{ name: 'x', kind: 'deployment', image: 'sai-worker', manifests: ['a'], manual: 'false' }] };
  assert.throws(() => validateRepoConfig(bad), /manual must be a boolean/);
});
test('rejects non-boolean disabled', () => {
  const bad = { ...valid, workloads: [{ name: 'x', kind: 'deployment', image: 'sai-worker', manifests: ['a'], disabled: 'true' }] };
  assert.throws(() => validateRepoConfig(bad), /disabled must be a boolean/);
});
test('accepts manual: true', () => {
  const ok = { ...valid, workloads: [{ name: 'x', kind: 'deployment', image: 'sai-worker', manifests: ['a'], manual: true }] };
  assert.equal(validateRepoConfig(ok).workloads[0].manual, true);
});
test('rejects non-boolean defaults.ciGate', () => {
  const bad = { ...valid, defaults: { ciGate: 'true' } };
  assert.throws(() => validateRepoConfig(bad), /ciGate must be a boolean/);
});
test('accepts defaults.ciGate true', () => {
  assert.doesNotThrow(() => validateRepoConfig({ ...valid, defaults: { ciGate: true } }));
});
