const { test } = require('node:test');
const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const path = require('node:path');
const { version } = require('../../package.json');
test('dray version prints the package version', () => {
  const bin = path.join(__dirname, '..', '..', 'bin', 'dray.js');
  const out = execFileSync('node', [bin, 'version'], { encoding: 'utf8' });
  assert.match(out, /dray \d+\.\d+\.\d+/);
});
test('dray --version prints the package version', () => {
  const bin = path.join(__dirname, '..', '..', 'bin', 'dray.js');
  assert.equal(execFileSync('node', [bin, '--version'], { encoding: 'utf8' }).trim(), version);
});
test('dray -v prints the package version', () => {
  const bin = path.join(__dirname, '..', '..', 'bin', 'dray.js');
  assert.equal(execFileSync('node', [bin, '-v'], { encoding: 'utf8' }).trim(), version);
});
