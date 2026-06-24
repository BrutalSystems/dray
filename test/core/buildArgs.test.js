const { test } = require('node:test'); const assert = require('node:assert/strict');
const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
const { resolveBuildArgs, _withRun } = require('../../src/core/buildArgs');

test('no buildArgs → empty', async () => { assert.deepEqual(await resolveBuildArgs({}, '/x'), []); });

test('reads envFile, filters by prefix, strips quotes, skips comments/blanks', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-ba-'));
  fs.writeFileSync(path.join(dir, '.env.production'),
    'VITE_A=1\nVITE_B="two"\nVITE_C=\'three\'\nOTHER=nope\n# comment\n\n');
  const args = await resolveBuildArgs({ buildArgs: { envFile: '.env.production', prefix: 'VITE_' } }, dir);
  assert.deepEqual(args.sort(), ['VITE_A=1', 'VITE_B=two', 'VITE_C=three']);
});

test('missing envFile → empty (no throw)', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-ba-'));
  assert.deepEqual(await resolveBuildArgs({ buildArgs: { envFile: '.env.production' } }, dir), []);
});

test('decrypts sopsEnvFile in memory via sops, filters by prefix', async () => {
  const calls = [];
  _withRun(async (cmd, args, opts) => {
    calls.push({ cmd, args, opts });
    return { code: 0, stdout: 'VITE_X=1\nVITE_Y="two"\nSECRET=nope\n' };
  });
  const args = await resolveBuildArgs(
    { buildArgs: { sopsEnvFile: 'secrets.env', prefix: 'VITE_' } }, '/repo');
  assert.deepEqual(args.sort(), ['VITE_X=1', 'VITE_Y=two']);
  // decrypts the right file, relative to the repo cwd, capturing stdout
  assert.equal(calls[0].cmd, 'sops');
  assert.deepEqual(calls[0].args, ['-d', '--output-type', 'dotenv', 'secrets.env']);
  assert.equal(calls[0].opts.cwd, '/repo');
  assert.equal(calls[0].opts.capture, true);
});

test('dryRun → passes dryRun through to sops, never spawns, returns []', async () => {
  let seen;
  // mirrors exec.run's dryRun contract: prints, returns empty stdout
  _withRun(async (cmd, args, opts) => { seen = opts; return { code: 0, stdout: '' }; });
  const args = await resolveBuildArgs(
    { buildArgs: { sopsEnvFile: 'secrets.env', prefix: 'VITE_' } }, '/repo', { dryRun: true });
  assert.deepEqual(args, []);
  assert.equal(seen.dryRun, true);
});
