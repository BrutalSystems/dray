const { test } = require('node:test'); const assert = require('node:assert/strict');
const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
const { resolveBuildArgs } = require('../../src/core/buildArgs');

test('no buildArgs → empty', () => { assert.deepEqual(resolveBuildArgs({}, '/x'), []); });

test('reads envFile, filters by prefix, strips quotes, skips comments/blanks', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-ba-'));
  fs.writeFileSync(path.join(dir, '.env.production'),
    'VITE_A=1\nVITE_B="two"\nVITE_C=\'three\'\nOTHER=nope\n# comment\n\n');
  const args = resolveBuildArgs({ buildArgs: { envFile: '.env.production', prefix: 'VITE_' } }, dir);
  assert.deepEqual(args.sort(), ['VITE_A=1', 'VITE_B=two', 'VITE_C=three']);
});

test('missing envFile → empty (no throw)', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-ba-'));
  assert.deepEqual(resolveBuildArgs({ buildArgs: { envFile: '.env.production' } }, dir), []);
});
