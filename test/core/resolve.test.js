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
