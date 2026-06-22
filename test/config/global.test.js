const { test } = require('node:test');
const assert = require('node:assert/strict');
const { parseGlobalConfig } = require('../../src/config/global');
test('missing file yields empty defaults', () => { assert.deepEqual(parseGlobalConfig(null), { defaults: {} }); });
test('parses defaults', () => { assert.deepEqual(parseGlobalConfig('{"defaults":{"region":"us-east-2"}}'), { defaults: { region: 'us-east-2' } }); });
