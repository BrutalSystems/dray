const { test } = require('node:test'); const assert = require('node:assert/strict');
const path = require('node:path');
const { currentSha, isDirty, lsRemote } = require('../../src/primitives/git');
test('currentSha returns a short sha', async () => { assert.match(await currentSha(path.join(__dirname, '..', '..')), /^[0-9a-f]{7,40}$/); });
test('isDirty returns a boolean', async () => { assert.equal(typeof await isDirty(path.join(__dirname, '..', '..')), 'boolean'); });
test('lsRemote returns the remote HEAD short sha (ls-remote works on a local path)', async () => {
  const repo = path.join(__dirname, '..', '..');
  const sha = await lsRemote({ git: repo, ref: 'HEAD' });
  assert.match(sha, /^[0-9a-f]{7}$/);
  assert.ok((await currentSha(repo)).startsWith(sha));   // matches this repo's HEAD
});
