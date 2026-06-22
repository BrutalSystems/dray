const { test } = require('node:test'); const assert = require('node:assert/strict');
const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
const { needsDepsRebuild } = require('../../src/core/depsCache');
function setup() {
  const repo = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-repo-'));
  const state = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-state-'));
  fs.writeFileSync(path.join(repo, 'uv.lock'), 'v1'); return { repo, state };
}
test('no depsImage → false', () => { const { repo, state } = setup(); assert.equal(needsDepsRebuild({ name: 'x' }, repo, 'sai', state), false); });
test('first true, second false, change true', () => {
  const { repo, state } = setup(); const img = { name: 'sai-worker', depsImage: { rebuildOn: ['uv.lock'] } };
  assert.equal(needsDepsRebuild(img, repo, 'sai', state), true);
  assert.equal(needsDepsRebuild(img, repo, 'sai', state), false);
  fs.writeFileSync(path.join(repo, 'uv.lock'), 'v2');
  assert.equal(needsDepsRebuild(img, repo, 'sai', state), true);
});
test('same image name in different repos do not collide', () => {
  const { repo, state } = setup(); const img = { name: 'app', depsImage: { rebuildOn: ['uv.lock'] } };
  assert.equal(needsDepsRebuild(img, repo, 'repoA', state), true);
  assert.equal(needsDepsRebuild(img, repo, 'repoB', state), true);
});
