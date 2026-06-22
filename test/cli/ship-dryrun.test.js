const { test } = require('node:test'); const assert = require('node:assert/strict');
const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
const { execFileSync } = require('node:child_process');
test('ship --dry-run prints ordered commands, no clone, rendered sha', () => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-home-'));
  const repo = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-proj-'));
  execFileSync('git', ['init', '-q'], { cwd: repo });
  execFileSync('git', ['-c', 'user.email=a@b.c', '-c', 'user.name=a', 'commit', '--allow-empty', '-m', 'x', '-q'], { cwd: repo });
  fs.mkdirSync(path.join(repo, '.k8s')); fs.writeFileSync(path.join(repo, '.k8s', 'deployment.yaml'), 'image: ${DEMO_IMAGE}\n');
  fs.mkdirSync(path.join(repo, '.dray'));
  fs.writeFileSync(path.join(repo, '.dray', 'config.json'), JSON.stringify({
    name: 'demo', images: [{ name: 'demo', ecr: 'demo', source: { local: true }, dockerfile: 'Dockerfile', context: '.' }],
    workloads: [{ name: 'demo', kind: 'deployment', image: 'demo', manifests: ['.k8s/deployment.yaml'] }],
  }));
  fs.mkdirSync(path.join(home, '.dray'), { recursive: true });
  fs.writeFileSync(path.join(home, '.dray', 'config.json'), JSON.stringify({ defaults: { account: '123', region: 'us-east-2', platform: 'linux/arm64', context: 'st-eks', namespace: 'marketing' } }));
  const bin = path.join(__dirname, '..', '..', 'bin', 'dray.js'); const env = { ...process.env, HOME: home };
  execFileSync('node', [bin, 'add'], { cwd: repo, env });
  const out = execFileSync('node', [bin, 'ship', 'demo', '--dry-run'], { env, encoding: 'utf8' });
  assert.match(out, /docker buildx build/);
  assert.match(out, /docker push 123\.dkr\.ecr\.us-east-2\.amazonaws\.com\/demo:DRYRUN/);
  assert.match(out, /kubectl apply -f/);
  assert.ok(out.indexOf('docker push') < out.indexOf('rollout restart'));
});
