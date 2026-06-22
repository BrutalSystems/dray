const fs = require('node:fs'); const path = require('node:path'); const { REGISTRY } = require('../constants');
function loadRegistry(file = REGISTRY) { return fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : {}; }
function saveRegistry(file = REGISTRY, reg = {}) { fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, JSON.stringify(reg, null, 2)); }
function addRepo(file, { name, path: p, config }) { const r = loadRegistry(file); r[name] = { path: p, config, addedAt: new Date().toISOString() }; saveRegistry(file, r); return r[name]; }
function getRepo(file, name) { return loadRegistry(file)[name]; }
function removeRepo(file, name) { const r = loadRegistry(file); delete r[name]; saveRegistry(file, r); }
function allRepos(file) { return loadRegistry(file); }
module.exports = { loadRegistry, saveRegistry, addRepo, getRepo, removeRepo, allRepos };
