const { test } = require('node:test'); const assert = require('node:assert/strict');
const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
const { execFileSync } = require('node:child_process');
test('init → add → list', () => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-home-'));
  const repo = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-proj-'));
  fs.mkdirSync(path.join(repo, '.dray'));
  fs.writeFileSync(path.join(repo, '.dray', 'config.json'), JSON.stringify({
    name: 'demo', images: [{ name: 'demo', ecr: 'demo', source: { local: true } }],
    workloads: [{ name: 'demo', kind: 'deployment', image: 'demo', manifests: ['.k8s/demo.yaml'] }],
  }));
  const bin = path.join(__dirname, '..', '..', 'bin', 'dray.js'); const env = { ...process.env, HOME: home };
  execFileSync('node', [bin, 'add'], { cwd: repo, env });
  assert.match(execFileSync('node', [bin, 'list'], { env, encoding: 'utf8' }), /demo/);
});
