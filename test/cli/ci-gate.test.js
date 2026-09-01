const { test } = require('node:test'); const assert = require('node:assert/strict');
const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
const { execFileSync, spawnSync } = require('node:child_process');

const BIN = path.join(__dirname, '..', '..', 'bin', 'dray.js');
const git = (cwd, ...a) => execFileSync('git', ['-c', 'user.email=a@b.c', '-c', 'user.name=a', ...a], { cwd, stdio: 'pipe' });

// A repo registered with the gate armed, plus a PATH of stub binaries: `gh`
// answers with whatever runs the test wants, and docker/aws are no-ops so a
// gate that PASSES can be observed without a real build.
function scaffold(runs, { ciGate = true, githubActions = '' } = {}) {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-home-'));
  const repo = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-proj-'));
  const bin = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-bin-'));
  fs.mkdirSync(path.join(repo, '.dray')); fs.mkdirSync(path.join(repo, '.k8s'));
  fs.writeFileSync(path.join(repo, 'Dockerfile'), 'FROM scratch\n');
  fs.writeFileSync(path.join(repo, '.k8s', 'deployment.yaml'), 'image: ${DEMO_IMAGE}\n');
  fs.writeFileSync(path.join(repo, '.dray', 'config.json'), JSON.stringify({
    name: 'demo', defaults: { ciGate },
    images: [{ name: 'demo', ecr: 'demo', source: { local: true }, dockerfile: 'Dockerfile', context: '.' }],
    workloads: [{ name: 'demo', kind: 'deployment', image: 'demo', manifests: ['.k8s/deployment.yaml'] }],
  }));
  git(repo, 'init', '-q'); git(repo, 'add', '-A'); git(repo, 'commit', '-q', '-m', 'x');
  const sha = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8' }).trim();
  fs.mkdirSync(path.join(home, '.dray'), { recursive: true });
  fs.writeFileSync(path.join(home, '.dray', 'config.json'), JSON.stringify({ defaults: {
    account: '123', region: 'us-east-2', platform: 'linux/arm64', context: 'st-eks', namespace: 'marketing' } }));
  const json = JSON.stringify(runs(sha)).split("'").join("'\\''");
  fs.writeFileSync(path.join(bin, 'gh'), `#!/bin/sh\nprintf '%s' '${json}'\n`, { mode: 0o755 });
  for (const stub of ['docker', 'aws']) fs.writeFileSync(path.join(bin, stub), '#!/bin/sh\nexit 0\n', { mode: 0o755 });
  // GITHUB_ACTIONS is pinned rather than inherited: dray's own CI sets it, and
  // the gate self-disables there -- which would silently pass every test below.
  const env = { ...process.env, HOME: home, PATH: `${bin}:${process.env.PATH}`, GITHUB_ACTIONS: githubActions };
  // process.execPath, not "node": a version-manager shim would follow the fake HOME.
  // spawnSync, not execFileSync: the gate reports on stderr, which execFileSync drops.
  const dray = (...args) => {
    const r = spawnSync(process.execPath, [BIN, ...args], { env, encoding: 'utf8', cwd: repo });
    return { code: r.status, out: `${r.stdout || ''}${r.stderr || ''}` };
  };
  dray('add', repo);
  return dray;
}
const run = (sha, over = {}) => ({ headSha: sha, status: 'completed', conclusion: 'success', workflowName: 'ci', url: 'https://x/1', ...over });

test('push is blocked when CI failed for HEAD', () => {
  const dray = scaffold((sha) => [run(sha, { conclusion: 'failure' })]);
  const { code, out } = dray('push', 'demo');
  assert.notEqual(code, 0);
  assert.match(out, /CI gate blocked this deploy/);
  assert.match(out, /--skip-ci-check/);
});
test('push is blocked while CI is still running', () => {
  const { code, out } = scaffold((sha) => [run(sha, { status: 'in_progress', conclusion: null })])('push', 'demo');
  assert.notEqual(code, 0); assert.match(out, /still running/);
});
test('push is blocked when no run covers HEAD or any ancestor', () => {
  const { code, out } = scaffold(() => [])('push', 'demo');
  assert.notEqual(code, 0); assert.match(out, /no CI run found/);
});
test('push proceeds when CI is green', () => {
  const { code, out } = scaffold((sha) => [run(sha)])('push', 'demo');
  assert.equal(code, 0); assert.match(out, /CI green/);
});
test('build is ungated -- it publishes nothing', () => {
  const { code, out } = scaffold((sha) => [run(sha, { conclusion: 'failure' })])('build', 'demo');
  assert.equal(code, 0); assert.doesNotMatch(out, /CI gate blocked/);
});
test('--dry-run does not consult CI', () => {
  const { code, out } = scaffold((sha) => [run(sha, { conclusion: 'failure' })])('push', 'demo', '--dry-run');
  assert.equal(code, 0); assert.match(out, /docker push/);
});
test('--skip-ci-check overrides a red suite, loudly', () => {
  const { code, out } = scaffold((sha) => [run(sha, { conclusion: 'failure' })])('push', 'demo', '--skip-ci-check');
  assert.equal(code, 0); assert.match(out, /CI gate SKIPPED/);
});
test('the gate self-disables inside GitHub Actions', () => {
  const dray = scaffold((sha) => [run(sha, { conclusion: 'failure' })], { githubActions: 'true' });
  const { code, out } = dray('push', 'demo');
  assert.equal(code, 0); assert.doesNotMatch(out, /CI gate blocked/);
});
// The default for the ~15 already-registered repos: unchanged behavior. The gh
// stub returns unparseable output, so consulting it at all would fail the push.
test('a repo without ciGate never consults CI', () => {
  const dray = scaffold(() => 'not json', { ciGate: false });
  const { code, out } = dray('push', 'demo');
  assert.equal(code, 0);
  assert.doesNotMatch(out, /CI (green|gate)/);
});
