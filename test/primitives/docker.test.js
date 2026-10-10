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

// --- re-ship of a commit whose image is already in ECR (2026-10-10) ---
// A second ship of the same commit used to re-push :<sha> with a NEW build, silently
// changing what that tag means (bs-jobs-service:642d56c, Sept 2026). With ECR tag
// immutability that push is rejected outright. Either way the right move is: keep the
// image ECR already has, and point :latest at IT, not at the fresh local rebuild.
const ECR_ARGS = { ecrUri: 'r/agent', ecr: 'agent', region: 'us-east-2', profile: 'st' };
const NOT_FOUND = { code: 254, stdout: '', stderr: "An error occurred (ImageNotFoundException) when calling the DescribeImages operation: The image with imageId {imageTag:'abc'} does not exist" };
const IMMUTABLE = "tag invalid: The image tag 'abc' already exists in the 'agent' repository and cannot be overwritten because the repository is immutable.";
test('pushImage does not re-push a :<sha> tag that already exists in ECR', async () => {
  const calls = []; docker._withRun(async (c, args) => { calls.push([c, args[1] || args[0]]); return { code: 0, stdout: '{}', stderr: '' }; });
  await docker.pushImage({ ...ECR_ARGS, sha: 'abc' });
  assert.deepEqual(calls, [['aws', 'describe-images']]);
});
test('pushImage pushes when ECR says the :<sha> tag does not exist', async () => {
  const calls = []; docker._withRun(async (c, args) => { calls.push([c, ...args.slice(0, 2)]); return c === 'aws' ? NOT_FOUND : { code: 0 }; });
  await docker.pushImage({ ...ECR_ARGS, sha: 'abc' });
  assert.deepEqual(calls, [['aws', 'ecr', 'describe-images'], ['docker', 'push', 'r/agent:abc']]);
});
test('pushImage points :latest at the EXISTING image in ECR, not the local rebuild', async () => {
  const calls = []; docker._withRun(async (c, args) => {
    calls.push([c, args[1] || args[0]]);
    if (args[1] === 'batch-get-image') return { code: 0, stdout: '{"manifest":1}\n', stderr: '' };
    return { code: 0, stdout: '{}', stderr: '' };
  });
  await docker.pushImage({ ...ECR_ARGS, sha: 'abc', latest: true });
  assert.deepEqual(calls, [['aws', 'describe-images'], ['aws', 'batch-get-image'], ['aws', 'put-image']]);
});
test('pushImage treats ":latest already points there" as success', async () => {
  docker._withRun(async (c, args) => {
    if (args[1] === 'put-image') return { code: 254, stdout: '', stderr: 'An error occurred (ImageAlreadyExistsException) when calling the PutImage operation: Image with digest sha256:x and tag latest already exists' };
    if (args[1] === 'batch-get-image') return { code: 0, stdout: '{"manifest":1}', stderr: '' };
    return { code: 0, stdout: '{}', stderr: '' };
  });
  await docker.pushImage({ ...ECR_ARGS, sha: 'abc', latest: true });
});
test('pushImage treats an immutable-tag rejection on push as "already in ECR"', async () => {
  const calls = []; let described = 0;
  docker._withRun(async (c, args) => {
    calls.push([c, c === 'docker' ? args[0] : args[1]]);
    if (c === 'aws' && args[1] === 'describe-images') { described += 1; return described === 1 ? NOT_FOUND : { code: 0, stdout: '{}', stderr: '' }; }
    if (c === 'docker' && args[0] === 'push') throw new Error(`docker push r/agent:abc failed (exit 1)\n${IMMUTABLE}`);
    return { code: 0 };
  });
  await docker.pushImage({ ...ECR_ARGS, sha: 'abc' });
  assert.deepEqual(calls, [['aws', 'describe-images'], ['docker', 'push'], ['aws', 'describe-images']]);
});
test('pushImage still pushes a dirty build without asking ECR', async () => {
  const calls = []; docker._withRun(async (c, args) => { calls.push([c, ...args]); return { code: 0 }; });
  await docker.pushImage({ ...ECR_ARGS, sha: 'abc-dirty', latest: true });
  assert.deepEqual(calls, [['docker', 'push', 'r/agent:abc-dirty']]);
});
test('pushImage surfaces a denied ECR lookup instead of pushing blind', async () => {
  const calls = []; docker._withRun(async (c, args) => { calls.push(c); return { code: 254, stdout: '', stderr: 'An error occurred (AccessDeniedException) when calling the DescribeImages operation: not authorized to perform: ecr:DescribeImages' }; });
  await assert.rejects(docker.pushImage({ ...ECR_ARGS, sha: 'abc' }), /ecr:DescribeImages/);
  assert.deepEqual(calls, ['aws']);
});
test('pushImage on dry run prints the push without asking ECR', async () => {
  const calls = []; docker._withRun(async (c, args) => { calls.push([c, ...args]); return { code: 0 }; });
  await docker.pushImage({ ...ECR_ARGS, sha: 'abc', dryRun: true });
  assert.deepEqual(calls, [['docker', 'push', 'r/agent:abc']]);
});
