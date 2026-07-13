const fs = require('node:fs'); const path = require('node:path'); const { REGISTRY } = require('../constants');
const { loadRepoConfig } = require('./repo');
function loadRegistry(file = REGISTRY) { return fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : {}; }
function saveRegistry(file = REGISTRY, reg = {}) { fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, JSON.stringify(reg, null, 2)); }
function addRepo(file, { name, path: p, config }) { const r = loadRegistry(file); r[name] = { path: p, config, addedAt: new Date().toISOString() }; saveRegistry(file, r); return r[name]; }
function getRepo(file, name) { return loadRegistry(file)[name]; }
function removeRepo(file, name) { const r = loadRegistry(file); delete r[name]; saveRegistry(file, r); }
// The registry caches a copy of each repo's config. Auto-reload it when the
// live .dray/config.json is newer than that snapshot, so edits take effect
// without a manual `dray reload`. Falls back to the snapshot when the repo
// isn't checked out here (stat throws) or its config is momentarily invalid
// (loadRepoConfig throws) — a broken repo must not break `list`/`--all`.
function refreshEntry(entry) {
  try {
    const mtime = Math.floor(fs.statSync(path.join(entry.path, '.dray', 'config.json')).mtimeMs);
    if (mtime <= (Date.parse(entry.addedAt) || 0)) return entry;
    return { ...entry, config: loadRepoConfig(entry.path), addedAt: new Date(mtime).toISOString() };
  } catch { return entry; }
}
function allRepos(file = REGISTRY) {
  const reg = loadRegistry(file); let changed = false;
  for (const [n, e] of Object.entries(reg)) { const f = refreshEntry(e); if (f !== e) { reg[n] = f; changed = true; } }
  if (changed) saveRegistry(file, reg);
  return reg;
}
module.exports = { loadRegistry, saveRegistry, addRepo, getRepo, removeRepo, allRepos };
