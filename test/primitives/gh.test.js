const { test } = require('node:test'); const assert = require('node:assert/strict');
const gh = require('../../src/primitives/gh');

test('recentRuns parses gh JSON', async () => {
  let a; gh._withRun(async (c, args) => { a = args; return { code: 0, stdout: '[{"headSha":"abc","status":"completed","conclusion":"success"}]' }; });
  const runs = await gh.recentRuns('/x', 5);
  assert.deepEqual(runs, [{ headSha: 'abc', status: 'completed', conclusion: 'success' }]);
  assert.ok(a.join(' ').includes('--limit 5'));
  assert.ok(a.join(' ').includes('headSha,status,conclusion,workflowName,url'));
});
// Everything below must THROW. A gh that cannot answer must never look like a
// green suite -- the gate turns a throw into a block, but a [] into a pass.
test('recentRuns rejects a missing gh binary', async () => {
  gh._withRun(async () => { throw new Error('spawn gh ENOENT'); });
  await assert.rejects(() => gh.recentRuns('/x'), /not installed/);
});
test('recentRuns rejects an unauthenticated gh', async () => {
  gh._withRun(async () => ({ code: 1, stdout: '', stderr: 'gh auth login required' }));
  await assert.rejects(() => gh.recentRuns('/x'), /not authenticated/);
});
test('recentRuns rejects any other gh failure', async () => {
  gh._withRun(async () => ({ code: 1, stdout: '', stderr: 'HTTP 503' }));
  await assert.rejects(() => gh.recentRuns('/x'), /HTTP 503/);
});
test('recentRuns rejects unparseable output', async () => {
  gh._withRun(async () => ({ code: 0, stdout: 'not json' }));
  await assert.rejects(() => gh.recentRuns('/x'), /unparseable/);
});
test('recentCommits returns shas newest first', async () => {
  let a; gh._withRun(async (c, args) => { a = args; return { code: 0, stdout: 'aaa\nbbb\nccc\n' }; });
  assert.deepEqual(await gh.recentCommits('/x', 3), ['aaa', 'bbb', 'ccc']);
  assert.deepEqual(a, ['rev-list', '-n', '3', 'HEAD']);
});
