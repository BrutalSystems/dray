const { test } = require('node:test'); const assert = require('node:assert/strict');
const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path'); const { execFileSync } = require('node:child_process');

function setup() {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-home-'));
  const repo = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-proj-'));
  fs.mkdirSync(path.join(repo, '.dray'));
  fs.writeFileSync(path.join(repo, '.dray', 'config.json'), JSON.stringify({
    name: 'web',
    pilets: {
      secretsFile: 'secrets.env',
      command: ['npm', 'run', 'publish:feed', '--', '--pilet', '{name}'],
      names: ['presence', 'agent'],
    },
  }));
  const bin = path.join(__dirname, '..', '..', 'bin', 'dray.js');
  const env = { ...process.env, HOME: home };
  execFileSync('node', [bin, 'add'], { cwd: repo, env });
  return { bin, env };
}

test('publish <repo>:<pilet> --dry-run substitutes {name} and wraps in sops', () => {
  const { bin, env } = setup();
  const out = execFileSync('node', [bin, 'publish', 'web:presence', '--dry-run'], { env, encoding: 'utf8' });
  assert.match(out, /sops exec-env secrets\.env -- npm run publish:feed -- --pilet presence/);
  assert.ok(!out.includes('{name}'));
});

test('publish <repo> --dry-run publishes all configured pilets', () => {
  const { bin, env } = setup();
  const out = execFileSync('node', [bin, 'publish', 'web', '--dry-run'], { env, encoding: 'utf8' });
  assert.match(out, /--pilet presence/);
  assert.match(out, /--pilet agent/);
});
