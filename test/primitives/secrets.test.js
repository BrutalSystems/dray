const { test } = require('node:test'); const assert = require('node:assert/strict');
const secrets = require('../../src/primitives/secrets');
test('sops-manifest decrypts and applies', async () => {
  const calls = []; secrets._withRun(async (c, a) => { calls.push([c, a]); return { code: 0 }; });
  await secrets.syncSecret({ name: 'sai-secrets', kind: 'sops-manifest', file: 'x.enc.yaml' },
    { repoPath: '/x', context: 'st-eks', namespace: 'marketing' });
  assert.equal(calls[0][0], 'bash');
  assert.match(calls[0][1].join(' '), /sops -d x\.enc\.yaml \| kubectl apply -f - -n marketing --context st-eks/);
});
test('literal-from-env applies then annotates', async () => {
  const calls = []; secrets._withRun(async (c, a) => { calls.push(a.join(' ')); return { code: 0 }; });
  await secrets.syncSecret({ name: 'mongodb-app', kind: 'literal-from-env', file: '.env', reflector: { 'reflector.v1.k8s.io/reflection-allowed': 'true' } },
    { repoPath: '/x', context: 'st-eks', namespace: 'marketing' });
  assert.ok(calls.some((c) => /from-env-file=\.env/.test(c)));
  assert.ok(calls.some((c) => /annotate secret mongodb-app reflector/.test(c)));
});
