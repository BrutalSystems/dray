const { test } = require('node:test'); const assert = require('node:assert/strict');
const docker = require('../../src/primitives/docker');
test('buildImage tags sha and loads', async () => {
  let a; docker._withRun(async (c, args) => { a = args; return { code: 0 }; });
  await docker.buildImage({ ecrUri: 'r/agent', sha: 'abc', dockerfile: 'D', context: '.', platform: 'linux/arm64', cwd: '/x' });
  assert.ok(a.includes('r/agent:abc') && a.includes('--platform') && a.includes('linux/arm64') && a.includes('--load'));
});
test('buildImage passes build args', async () => {
  let a; docker._withRun(async (c, args) => { a = args; return { code: 0 }; });
  await docker.buildImage({ ecrUri: 'r/w', sha: 's', dockerfile: 'D', context: '.', platform: 'p', cwd: '/x', buildArgs: ['VITE_A=1', 'VITE_B=two'] });
  assert.ok(a.join(' ').includes('--build-arg VITE_A=1'));
  assert.ok(a.join(' ').includes('--build-arg VITE_B=two'));
});

test('pushImage pushes sha tag', async () => {
  let a; docker._withRun(async (c, args) => { a = args; return { code: 0 }; });
  await docker.pushImage({ ecrUri: 'r/agent', sha: 'abc' });
  assert.deepEqual(a, ['push', 'r/agent:abc']);
});
test('ecrLogin pipes get-login-password into docker login with profile', async () => {
  let a; docker._withRun(async (c, args) => { a = [c, args]; return { code: 0 }; });
  await docker.ecrLogin({ account: '123', region: 'us-east-2', profile: 'st' });
  assert.equal(a[0], 'bash');
  assert.match(a[1].join(' '), /aws ecr get-login-password .*--profile st .*docker login .*123\.dkr\.ecr\.us-east-2/);
});
