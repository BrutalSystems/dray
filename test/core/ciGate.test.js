const { test } = require('node:test'); const assert = require('node:assert/strict');
const { evaluate, gatedRepos, assertCiGreen } = require('../../src/core/ciGate');

const HEAD = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const P1 = 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';
const P2 = 'cccccccccccccccccccccccccccccccccccccccc';
const commits = [HEAD, P1, P2];
const run = (headSha, over = {}) => ({ headSha, status: 'completed', conclusion: 'success', workflowName: 'ci', url: 'https://x/1', ...over });

test('green run for HEAD passes', () => {
  const v = evaluate({ commits, runs: [run(HEAD)] });
  assert.equal(v.ok, true); assert.equal(v.reason, 'green'); assert.equal(v.sha, HEAD.slice(0, 7));
});
test('failed run for HEAD blocks', () => {
  const v = evaluate({ commits, runs: [run(HEAD, { conclusion: 'failure' })] });
  assert.equal(v.ok, false); assert.equal(v.reason, 'failed'); assert.match(v.detail, /ci=failure/);
});
// The 2026-09-01 race: the image was pushed while CI for that sha was still running.
test('in-progress run for HEAD blocks as pending', () => {
  const v = evaluate({ commits, runs: [run(HEAD, { status: 'in_progress', conclusion: null })] });
  assert.equal(v.ok, false); assert.equal(v.reason, 'pending'); assert.equal(v.url, 'https://x/1');
});
test('queued run blocks as pending too', () => {
  const v = evaluate({ commits, runs: [run(HEAD, { status: 'queued', conclusion: null })] });
  assert.equal(v.ok, false); assert.equal(v.reason, 'pending');
});
test('no runs at all blocks as absent', () => {
  const v = evaluate({ commits, runs: [] });
  assert.equal(v.ok, false); assert.equal(v.reason, 'absent');
});
// The docs-only-commit case: paths-ignore means those commits produce NO run,
// and the last verdict on covered code still stands.
test('walks back to the newest covered ancestor', () => {
  const v = evaluate({ commits, runs: [run(P2)] });
  assert.equal(v.ok, true); assert.equal(v.sha, P2.slice(0, 7));
});
test('walk-back stops at the NEWEST covered ancestor, not the first green one', () => {
  const v = evaluate({ commits, runs: [run(P1, { conclusion: 'failure' }), run(P2)] });
  assert.equal(v.ok, false); assert.equal(v.reason, 'failed'); assert.equal(v.sha, P1.slice(0, 7));
});
test('one failing workflow among several blocks the sha', () => {
  const v = evaluate({ commits, runs: [run(HEAD), run(HEAD, { workflowName: 'lint', conclusion: 'failure' })] });
  assert.equal(v.ok, false); assert.equal(v.reason, 'failed'); assert.match(v.detail, /lint=failure/);
});
for (const conclusion of ['skipped', 'neutral']) {
  test(`${conclusion} counts as passing`, () => {
    assert.equal(evaluate({ commits, runs: [run(HEAD, { conclusion })] }).ok, true);
  });
}
test('runs for unrelated shas are ignored', () => {
  const v = evaluate({ commits, runs: [run('dddddddddddddddddddddddddddddddddddddddd')] });
  assert.equal(v.ok, false); assert.equal(v.reason, 'absent');
});

const unit = (over = {}) => ({ repo: 'demo', repoPath: '/x/demo', image: { name: 'demo', source: { local: true } }, defaults: { ciGate: true }, ...over });

test('gatedRepos returns nothing when ciGate is unset', () => {
  assert.equal(gatedRepos([unit({ defaults: {} })]).size, 0);
});
test('gatedRepos dedupes by repoPath', () => {
  const g = gatedRepos([unit(), unit({ workload: 'other' })]);
  assert.deepEqual([...g], [['/x/demo', 'demo']]);
});
test('gatedRepos skips git-sourced images (their CI lives in another repo)', () => {
  const g = gatedRepos([unit({ image: { name: 'jobs', source: { git: 'git@github.com:o/r.git' } } })]);
  assert.equal(g.size, 0);
});
test('gatedRepos keeps a gated repo when only one of its units is git-sourced', () => {
  const g = gatedRepos([unit({ image: { name: 'jobs', source: { git: 'g' } } }), unit()]);
  assert.deepEqual([...g], [['/x/demo', 'demo']]);
});

const boom = { gh: { recentCommits: async () => { throw new Error('gh called'); }, recentRuns: async () => { throw new Error('gh called'); } } };
const quiet = () => {};

test('assertCiGreen is a no-op for ungated units', async () => {
  await assertCiGreen([unit({ defaults: {} })], { deps: boom, log: quiet });
});
test('assertCiGreen skips dry runs', async () => {
  await assertCiGreen([unit()], { dryRun: true, deps: boom, log: quiet });
});
// Inside GitHub Actions the gate would be waiting on the run that invoked it.
test('assertCiGreen skips when running inside GitHub Actions', async () => {
  const prev = process.env.GITHUB_ACTIONS; process.env.GITHUB_ACTIONS = 'true';
  try { await assertCiGreen([unit()], { deps: boom, log: quiet }); }
  finally { if (prev === undefined) delete process.env.GITHUB_ACTIONS; else process.env.GITHUB_ACTIONS = prev; }
});
test('assertCiGreen warns but proceeds with --skip-ci-check', async () => {
  const lines = [];
  await assertCiGreen([unit()], { skipCiCheck: true, deps: boom, log: (m) => lines.push(m) });
  assert.match(lines.join('\n'), /SKIPPED/);
});
test('assertCiGreen throws with the repo name and the override hint when CI is red', async () => {
  const deps = { gh: { recentCommits: async () => commits, recentRuns: async () => [run(HEAD, { conclusion: 'failure' })] } };
  await assert.rejects(() => assertCiGreen([unit()], { deps, log: quiet }),
    (err) => /demo: CI gate blocked/.test(err.message) && /--skip-ci-check/.test(err.message));
});
test('assertCiGreen passes when CI is green', async () => {
  const deps = { gh: { recentCommits: async () => commits, recentRuns: async () => [run(HEAD)] } };
  const lines = [];
  await assertCiGreen([unit()], { deps, log: (m) => lines.push(m) });
  assert.match(lines.join('\n'), /CI green/);
});
// A gh failure (missing binary, expired token) must block, never read as green.
test('assertCiGreen propagates a gh failure instead of passing', async () => {
  const deps = { gh: { recentCommits: async () => commits, recentRuns: async () => { throw new Error('gh CLI not installed (brew install gh)'); } } };
  await assert.rejects(() => assertCiGreen([unit()], { deps, log: quiet }), /gh CLI not installed/);
});
test('assertCiGreen refuses a dirty tree under --allow-dirty', async () => {
  const deps = { gh: boom.gh, git: { isDirty: async () => true } };
  await assert.rejects(() => assertCiGreen([unit()], { allowDirty: true, deps, log: quiet }), /working tree is dirty/);
});
