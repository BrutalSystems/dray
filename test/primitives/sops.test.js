const { test } = require('node:test'); const assert = require('node:assert/strict');
const sops = require('../../src/primitives/sops');
test('execEnv passes command as one shell-quoted string (sops 3.x form)', async () => {
  let a; sops._withRun(async (c, args) => { a = [c, args]; return { code: 0 }; });
  await sops.execEnv('secrets.env', ['uv', 'run', '--with', 'typer[all]', 'python3', 'pub.py', '--pilet', 'presence']);
  assert.equal(a[0], 'sops');
  assert.equal(a[1][0], 'exec-env');
  assert.equal(a[1][1], 'secrets.env');
  // single command arg, special chars quoted, no `--` separator
  assert.equal(a[1].length, 3);
  assert.equal(a[1][2], "uv run --with 'typer[all]' python3 pub.py --pilet presence");
});
