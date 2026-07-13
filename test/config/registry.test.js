const { test } = require('node:test'); const assert = require('node:assert/strict');
const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
const reg = require('../../src/config/registry');
const tmp = () => path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'dray-')), 'registry.json');
// A repo dir holding a live .dray/config.json with the given secrets[].
const repoWith = (secrets) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-repo-'));
  fs.mkdirSync(path.join(dir, '.dray'));
  fs.writeFileSync(path.join(dir, '.dray', 'config.json'), JSON.stringify({ name: 'demo', images: [], workloads: [], secrets }));
  return dir;
};
test('add then get', () => { const f = tmp(); reg.addRepo(f, { name: 'sai', path: '/x', config: {} }); assert.equal(reg.getRepo(f, 'sai').path, '/x'); });
test('remove', () => { const f = tmp(); reg.addRepo(f, { name: 'sai', path: '/x', config: {} }); reg.removeRepo(f, 'sai'); assert.equal(reg.getRepo(f, 'sai'), undefined); });
test('missing file → {}', () => { assert.deepEqual(reg.loadRegistry(tmp()), {}); });

test('allRepos auto-reloads when live config is newer than the cached snapshot', () => {
  const f = tmp(); const dir = repoWith([{ name: 'fresh', kind: 'sops-manifest', file: 'x' }]);
  // Cached snapshot predates the live file (stale) and is missing "fresh".
  reg.saveRegistry(f, { demo: { path: dir, config: { name: 'demo', secrets: [] }, addedAt: '2000-01-01T00:00:00.000Z' } });
  const names = reg.allRepos(f).demo.config.secrets.map((s) => s.name);
  assert.deepEqual(names, ['fresh']);
});

test('allRepos keeps the cached snapshot when the live config is not newer', () => {
  const f = tmp(); const dir = repoWith([{ name: 'ondisk', kind: 'sops-manifest', file: 'x' }]);
  // Cache is newer than the file → not stale → do not re-read.
  reg.saveRegistry(f, { demo: { path: dir, config: { name: 'demo', secrets: [{ name: 'cached' }] }, addedAt: '2999-01-01T00:00:00.000Z' } });
  assert.deepEqual(reg.allRepos(f).demo.config.secrets.map((s) => s.name), ['cached']);
});

test('allRepos keeps the cached snapshot when the repo is not checked out here', () => {
  const f = tmp();
  reg.saveRegistry(f, { demo: { path: '/no/such/repo', config: { name: 'demo', secrets: [{ name: 'cached' }] }, addedAt: '2000-01-01T00:00:00.000Z' } });
  assert.deepEqual(reg.allRepos(f).demo.config.secrets.map((s) => s.name), ['cached']);
});
