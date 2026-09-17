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
test('buildImage disables provenance attestations (EKS pulls single-arch images)', async () => {
  let a; docker._withRun(async (c, args) => { a = args; return { code: 0 }; });
  await docker.buildImage({ ecrUri: 'r/w', sha: 's', dockerfile: 'D', context: '.', platform: 'linux/arm64', cwd: '/x' });
  assert.ok(a.join(' ').includes('--provenance=false'));
});

test('pushImage pushes sha tag', async () => {
  let a; docker._withRun(async (c, args) => { a = args; return { code: 0 }; });
  await docker.pushImage({ ecrUri: 'r/agent', sha: 'abc' });
  assert.deepEqual(a, ['push', 'r/agent:abc']);
});
test('pushImage also tags + pushes :latest when latest is set', async () => {
  const calls = []; docker._withRun(async (c, args) => { calls.push([c, ...args]); return { code: 0 }; });
  await docker.pushImage({ ecrUri: 'r/agent', sha: 'abc', latest: true });
  assert.deepEqual(calls, [
    ['docker', 'push', 'r/agent:abc'],
    ['docker', 'tag', 'r/agent:abc', 'r/agent:latest'],
    ['docker', 'push', 'r/agent:latest'],
  ]);
});
test('pushImage never moves :latest onto a dirty build', async () => {
  const calls = []; docker._withRun(async (c, args) => { calls.push([c, ...args]); return { code: 0 }; });
  await docker.pushImage({ ecrUri: 'r/agent', sha: 'abc-dirty', latest: true });
  assert.deepEqual(calls, [['docker', 'push', 'r/agent:abc-dirty']]);
});
test('buildImage injects GIT_SHA build arg from the sha', async () => {
  let a; docker._withRun(async (c, args) => { a = args; return { code: 0 }; });
  await docker.buildImage({ ecrUri: 'r/w', sha: 'abc123', dockerfile: 'D', context: '.', platform: 'linux/arm64', cwd: '/x' });
  assert.ok(a.join(' ').includes('--build-arg GIT_SHA=abc123'));
});
test('explicit build args still apply alongside GIT_SHA', async () => {
  let a; docker._withRun(async (c, args) => { a = args; return { code: 0 }; });
  await docker.buildImage({ ecrUri: 'r/w', sha: 's', dockerfile: 'D', context: '.', platform: 'p', cwd: '/x', buildArgs: ['VITE_A=1'] });
  assert.ok(a.join(' ').includes('--build-arg GIT_SHA=s'));
  assert.ok(a.join(' ').includes('--build-arg VITE_A=1'));
});
test('ecrLogin pipes get-login-password into docker login with profile', async () => {
  let a; docker._withRun(async (c, args) => { a = [c, args]; return { code: 0 }; });
  await docker.ecrLogin({ account: '123', region: 'us-east-2', profile: 'st' });
  assert.equal(a[0], 'bash');
  assert.match(a[1].join(' '), /aws ecr get-login-password .*--profile st .*docker login .*123\.dkr\.ecr\.us-east-2/);
});
test('ensureRepo does nothing when the repo exists', async () => {
  const calls = []; docker._withRun(async (c, args) => { calls.push(args[1]); return { code: 0, stdout: '{}', stderr: '' }; });
  await docker.ensureRepo({ ecr: 'bs-x', region: 'us-east-2', profile: 'st' });
  assert.deepEqual(calls, ['describe-repositories']);
});
test('ensureRepo creates the repo only when ECR says it does not exist', async () => {
  const calls = []; docker._withRun(async (c, args) => {
    calls.push(args[1]);
    return args[1] === 'describe-repositories'
      ? { code: 254, stdout: '', stderr: 'An error occurred (RepositoryNotFoundException) when calling the DescribeRepositories operation: The repository with name \'bs-x\' does not exist' }
      : { code: 0, stdout: '', stderr: '' };
  });
  await docker.ensureRepo({ ecr: 'bs-x', region: 'us-east-2', profile: 'st' });
  assert.deepEqual(calls, ['describe-repositories', 'create-repository']);
});
test('ensureRepo surfaces a denied describe instead of trying to create', async () => {
  const calls = []; docker._withRun(async (c, args) => {
    calls.push(args[1]);
    return { code: 254, stdout: '', stderr: 'An error occurred (AccessDeniedException) when calling the DescribeRepositories operation: User: arn:aws:sts::1:assumed-role/X is not authorized to perform: ecr:DescribeRepositories' };
  });
  await assert.rejects(docker.ensureRepo({ ecr: 'bs-x', region: 'us-east-2', profile: 'st-bs' }), /ecr:DescribeRepositories/);
  assert.deepEqual(calls, ['describe-repositories']);
});
