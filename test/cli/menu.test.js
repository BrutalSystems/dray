const { test } = require('node:test'); const assert = require('node:assert/strict');
const { buildMenuTree } = require('../../src/cli/menu');
test('buildMenuTree lists repo:workload specs', () => {
  const reg = { sai: { path: '/x', config: { name: 'sai', workloads: [{ name: 'agent', kind: 'deployment', image: 'a', manifests: ['m'] }] } } };
  const t = buildMenuTree(reg);
  assert.deepEqual(t.repos[0].targets.map((x) => x.spec), ['sai:agent']);
  assert.ok(t.actions.includes('ship'));
});
