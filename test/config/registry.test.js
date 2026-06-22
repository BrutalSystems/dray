const { test } = require('node:test'); const assert = require('node:assert/strict');
const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
const reg = require('../../src/config/registry');
const tmp = () => path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'dray-')), 'registry.json');
test('add then get', () => { const f = tmp(); reg.addRepo(f, { name: 'sai', path: '/x', config: {} }); assert.equal(reg.getRepo(f, 'sai').path, '/x'); });
test('remove', () => { const f = tmp(); reg.addRepo(f, { name: 'sai', path: '/x', config: {} }); reg.removeRepo(f, 'sai'); assert.equal(reg.getRepo(f, 'sai'), undefined); });
test('missing file → {}', () => { assert.deepEqual(reg.loadRegistry(tmp()), {}); });
