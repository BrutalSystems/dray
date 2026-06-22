const { test } = require('node:test'); const assert = require('node:assert/strict');
const path = require('node:path');
const { currentSha, isDirty } = require('../../src/primitives/git');
test('currentSha returns a short sha', async () => { assert.match(await currentSha(path.join(__dirname, '..', '..')), /^[0-9a-f]{7,40}$/); });
test('isDirty returns a boolean', async () => { assert.equal(typeof await isDirty(path.join(__dirname, '..', '..')), 'boolean'); });
