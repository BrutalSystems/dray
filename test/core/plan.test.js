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

// End-to-end guard for the property `manual` exists to provide: a bare-repo ship
// (what CI runs) must not build the manual workload's image, apply its manifests,
// or -- critically -- run a secret step for it. A sops decrypt in CI needs an age
// key that CI deliberately does not have, so a leaked secret step fails the build.
const { resolveTargets } = require('../../src/core/resolve');
test('bare-repo ship plan omits a manual workload entirely, including its secret step', () => {
  const registry = { sift: { path: '/x/sift', config: {
    name: 'sift',
    images: [
      { name: 'sift-be', ecr: 'bs-sift-be', source: { local: true } },
      { name: 'jobs-service', ecr: 'bs-sift-jobs-service', source: { git: 'https://example.invalid/jobs-service' } },
    ],
    workloads: [
      { name: 'sift-be', kind: 'deployment', image: 'sift-be', manifests: ['.k8s/deployment.yaml'] },
      { name: 'sift-jobs-service', kind: 'deployment', image: 'jobs-service', manifests: ['.k8s/jobs-service/deployment.yaml'], manual: true, dependsOn: ['secret:jobs-secrets'] },
    ],
    secrets: [{ name: 'jobs-secrets', kind: 'sops-manifest', file: '.k8s/jobs-service/secret.sops.yaml' }],
  } } };
  const globalDefaults = { account: '123', region: 'us-east-2', context: 'st-eks', namespace: 'bs' };

  const ciSteps = planFor(resolveTargets({ registry, globalDefaults, spec: 'sift' }), 'ship');
  assert.equal(ciSteps.filter((s) => s.kind === 'secret').length, 0);
  assert.ok(!ciSteps.some((s) => s.label.includes('jobs-service')));

  // ...but an explicit manual deploy still gets the full plan, secret and all.
  const manualSteps = planFor(resolveTargets({ registry, globalDefaults, spec: 'sift:sift-jobs-service' }), 'ship');
  assert.equal(manualSteps.filter((s) => s.kind === 'secret').length, 1);
  assert.ok(manualSteps.some((s) => s.kind === 'push' && s.label.includes('jobs-service')));
});
