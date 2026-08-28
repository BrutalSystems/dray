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

test('rollout waits without restarting when asked not to restart', async () => {
  // `ship` applies a manifest stamped with a new image SHA, which makes the
  // Deployment controller start a rollout by itself. Issuing `rollout restart`
  // on top of that is a SECOND rollout: two ReplicaSets, two pod
  // replacements, twice the disruption window, per deploy.
  const calls = [];
  k._withRun(async (bin, args) => { calls.push(args.slice(0, 2).join(' ')); return {}; });
  await k.rollout({ deployment: 'api', context: 'c', namespace: 'n', restart: false });
  assert.deepEqual(calls, ['rollout status']);
});

test('rollout restarts by default', async () => {
  // The unchanged-SHA case still needs it: re-shipping the same commit to pick
  // up a changed Secret applies nothing, so the restart is the only thing that
  // cycles pods.
  const calls = [];
  k._withRun(async (bin, args) => { calls.push(args.slice(0, 2).join(' ')); return {}; });
  await k.rollout({ deployment: 'api', context: 'c', namespace: 'n' });
  assert.deepEqual(calls, ['rollout restart', 'rollout status']);
});
