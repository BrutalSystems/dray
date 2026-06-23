const { test } = require('node:test'); const assert = require('node:assert/strict');
const { planFor } = require('../../src/core/plan');
const unit = (img, workload, kind = 'deployment', dependsOn = [], _secrets = []) => ({
  repo: 'sai', image: { name: img, depsImage: img === 'sai-worker' ? { rebuildOn: ['uv.lock'] } : undefined },
  workload, kind, manifests: workload ? [`.k8s/${workload}.yaml`] : [], dependsOn, _secrets,
});
test('ship batches: pushes precede secrets precede apply precede rollout', () => {
  const units = [
    unit('sai-worker', 'sai-api', 'deployment', ['secret:sai-secrets'], [{ name: 'sai-secrets', kind: 'sops-manifest', file: 's' }]),
    unit('agent-service', 'agent-service'),
  ];
  const kinds = planFor(units, 'ship').map((s) => s.kind);
  assert.ok(kinds.lastIndexOf('push') < kinds.indexOf('secret'));
  assert.ok(kinds.indexOf('secret') < kinds.indexOf('apply'));
  assert.ok(kinds.lastIndexOf('apply') < kinds.indexOf('rollout'));
  assert.ok(kinds.includes('deps'));
});
test('secret step carries the resolved secret object', () => {
  const units = [unit('sai-worker', 'sai-api', 'deployment', ['secret:sai-secrets'], [{ name: 'sai-secrets', kind: 'sops-manifest', file: 's.enc.yaml' }])];
  const step = planFor(units, 'ship').find((s) => s.kind === 'secret');
  assert.deepEqual(step.secret, { name: 'sai-secrets', kind: 'sops-manifest', file: 's.enc.yaml' });
});
test('cronjob unit gets apply but no rollout', () => {
  const kinds = planFor([unit('aigateway-cost', 'rollup', 'cronjob')], 'ship').map((s) => s.kind);
  assert.ok(kinds.includes('apply')); assert.ok(!kinds.includes('rollout'));
});
test('no-image workload: apply + rollout, no build/push', () => {
  const unit = { repo: 'sai', image: null, workload: 'searxng', kind: 'deployment', manifests: ['.k8s/searxng/deployment.yaml'], dependsOn: [], _secrets: [] };
  const kinds = planFor([unit], 'ship').map((s) => s.kind);
  assert.ok(!kinds.includes('build') && !kinds.includes('push'), 'no build/push for image-less workload');
  assert.ok(kinds.includes('apply') && kinds.includes('rollout'), 'apply + rollout present');
});

test('build dedups shared image across workloads', () => {
  const units = [unit('sai-worker', 'sai-api'), unit('sai-worker', 'sai-api-warmworker')];
  assert.deepEqual(planFor(units, 'build').map((s) => s.kind), ['deps', 'build']);
});
