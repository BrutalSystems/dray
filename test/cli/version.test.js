const { test } = require('node:test');
const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const path = require('node:path');
test('dray version prints the package version', () => {
  const bin = path.join(__dirname, '..', '..', 'bin', 'dray.js');
  const out = execFileSync('node', [bin, 'version'], { encoding: 'utf8' });
  assert.match(out, /dray \d+\.\d+\.\d+/);
});
