const { test } = require('node:test');
const assert = require('node:assert/strict');
const { run } = require('../../src/primitives/exec');
test('capture returns stdout and zero code', async () => {
  const r = await run('node', ['-e', "process.stdout.write('hi')"], { capture: true });
  assert.equal(r.code, 0); assert.equal(r.stdout, 'hi');
});
test('dryRun does not execute', async () => {
  const r = await run('false', [], { dryRun: true }); assert.equal(r.code, 0);
});
test('nonzero exit throws with command in message', async () => {
  await assert.rejects(() => run('node', ['-e', 'process.exit(3)'], { capture: true }), /exit 3/);
});
