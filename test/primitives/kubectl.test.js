const { test } = require('node:test'); const assert = require('node:assert/strict');
const k = require('../../src/primitives/kubectl');
test('applyFile passes -f with context+namespace', async () => {
  let a; k._withRun(async (c, args) => { a = args; return { code: 0 }; });
  await k.applyFile({ file: '/tmp/x.yaml', context: 'st-eks', namespace: 'marketing' });
  assert.deepEqual(a, ['apply', '-f', '/tmp/x.yaml', '-n', 'marketing', '--context', 'st-eks']);
});
test('rolloutUndo targets the deployment', async () => {
  let a; k._withRun(async (c, args) => { a = args; return { code: 0 }; });
  await k.rolloutUndo({ deployment: 'sai-api', context: 'st-eks', namespace: 'marketing' });
  assert.ok(a.join(' ').includes('rollout undo deployment/sai-api'));
});
