const { test } = require('node:test'); const assert = require('node:assert/strict');
const sops = require('../../src/primitives/sops');
test('execEnv uses -- separator and preserves argv', async () => {
  let a; sops._withRun(async (c, args) => { a = [c, args]; return { code: 0 }; });
  await sops.execEnv('secrets.env', ['npm', 'run', 'publish:feed', '--', '--pilet', 'presence']);
  assert.equal(a[0], 'sops');
  assert.deepEqual(a[1], ['exec-env', 'secrets.env', '--', 'npm', 'run', 'publish:feed', '--', '--pilet', 'presence']);
});
