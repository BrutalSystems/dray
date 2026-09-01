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

// --- rolloutInProgress ------------------------------------------------------
// The signal that stops CI from rolling every deploy twice. It must read cluster state,
// because `dray apply` and `dray rollout` run as separate processes there.
const rip = (obj, code = 0) => {
  k._withRun(async () => ({ code, stdout: typeof obj === 'string' ? obj : JSON.stringify(obj) }));
  return k.rolloutInProgress({ deployment: 'api', context: 'c', namespace: 'n' });
};

test('rolloutInProgress: true when the controller has not observed the new spec', async () => {
  assert.equal(await rip({ metadata: { generation: 5 }, status: { observedGeneration: 4 },
    spec: { replicas: 2 } }), true);
});

test('rolloutInProgress: true while pods are still converging', async () => {
  assert.equal(await rip({ metadata: { generation: 5 }, status: { observedGeneration: 5, updatedReplicas: 1, availableReplicas: 1 },
    spec: { replicas: 2 } }), true);
});

test('rolloutInProgress: false when fully rolled out', async () => {
  // The standalone `dray rollout` case -- nothing in flight, so the restart is the only
  // thing that would cycle pods and must still happen.
  assert.equal(await rip({ metadata: { generation: 5 }, status: { observedGeneration: 5, updatedReplicas: 2, availableReplicas: 2 },
    spec: { replicas: 2 } }), false);
});

test('rolloutInProgress: false when kubectl fails', async () => {
  // Conservative: an unreadable deployment keeps the restart rather than silently doing
  // nothing.
  assert.equal(await rip({}, 1), false);
});

test('rolloutInProgress: false on unparseable output', async () => {
  assert.equal(await rip('not json at all'), false);
});

test('rolloutInProgress: false when generation is missing', async () => {
  assert.equal(await rip({ metadata: {}, status: {}, spec: {} }), false);
});

test('rolloutInProgress: absent replica counts read as zero, not as complete', async () => {
  // A freshly-scaled deployment reports no updatedReplicas/availableReplicas at all. Treating
  // undefined as "done" would restart straight into an in-flight rollout.
  assert.equal(await rip({ metadata: { generation: 2 }, status: { observedGeneration: 2 },
    spec: { replicas: 2 } }), true);
});

test('rolloutInProgress: asks for json, scoped to namespace and context', async () => {
  let args; k._withRun(async (bin, a) => { args = a; return { code: 0, stdout: '{}' }; });
  await k.rolloutInProgress({ deployment: 'api', context: 'st-eks', namespace: 'bs' });
  assert.deepEqual(args, ['get', 'deployment/api', '-o', 'json', '-n', 'bs', '--context', 'st-eks']);
});
