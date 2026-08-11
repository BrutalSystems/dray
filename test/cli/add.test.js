const { test } = require('node:test'); const assert = require('node:assert/strict');
const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
const { resolveRepoPath } = require('../../src/cli/commands/add');

// `dray add .` must store an ABSOLUTE path. Storing "." verbatim makes the entry
// resolve against whatever cwd dray runs in later, so two repos added that way
// collide -- `dray ship <a>` from repo b would deploy b under a's name.
test('resolveRepoPath makes a relative argument absolute', () => {
  const abs = resolveRepoPath('.');
  assert.ok(path.isAbsolute(abs), `expected absolute, got ${abs}`);
  assert.equal(abs, fs.realpathSync(process.cwd()));
});
test('resolveRepoPath leaves an absolute argument absolute', () => {
  const dir = fs.realpathSync(os.tmpdir());
  assert.equal(resolveRepoPath(dir), dir);
});
test('resolveRepoPath defaults to cwd when no argument is given', () => {
  assert.equal(resolveRepoPath(undefined), fs.realpathSync(process.cwd()));
});
