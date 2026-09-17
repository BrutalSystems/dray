let _run = require('./exec').run; function _withRun(fn) { _run = fn; }
function buildImage({ ecrUri, sha, dockerfile, context, platform, cwd, dryRun, buildArgs = [] }) {
  const args = ['buildx', 'build', '--platform', platform, '--provenance=false', '-f', dockerfile, '-t', `${ecrUri}:${sha}`];
  args.push('--build-arg', `GIT_SHA=${sha}`);
  for (const a of buildArgs) args.push('--build-arg', a);
  args.push('--load', context);
  return _run('docker', args, { cwd, dryRun });
}
// Push the immutable :<sha> tag. When the image opts into `latest: true`, also
// move a mutable :latest tag onto this build so workloads that reference
// <ecr>:latest (e.g. rarely-shipped cronjobs) track the newest build without
// being re-shipped. Never advance :latest for a dirty (`-dirty`) build.
async function pushImage({ ecrUri, sha, latest = false, dryRun }) {
  await _run('docker', ['push', `${ecrUri}:${sha}`], { dryRun });
  if (latest && !String(sha).endsWith('-dirty')) {
    await _run('docker', ['tag', `${ecrUri}:${sha}`, `${ecrUri}:latest`], { dryRun });
    await _run('docker', ['push', `${ecrUri}:latest`], { dryRun });
  }
}
function buildDeps(image, cwd, dryRun) {
  const d = image.depsImage;
  return _run('docker', ['buildx', 'build', '--platform', 'linux/arm64', '-f', d.dockerfile, '-t', d.tag, '--load', '.'], { cwd, dryRun });
}
function ecrLogin({ account, region, profile, dryRun }) {
  const reg = `${account}.dkr.ecr.${region}.amazonaws.com`; const prof = profile ? `--profile ${profile} ` : '';
  return _run('bash', ['-c', `aws ecr get-login-password --region ${region} ${prof}| docker login --username AWS --password-stdin ${reg}`], { dryRun });
}
async function ensureRepo({ ecr, account, region, profile, dryRun }) {
  const prof = profile ? ['--profile', profile] : [];
  const r = await _run('aws', ['ecr', 'describe-repositories', '--repository-names', ecr, '--region', region, ...prof], { capture: true, allowFail: true, dryRun });
  if (r.code === 0) return;
  // Only a genuinely missing repo should be created. Any other failure (most often a
  // profile lacking ecr:DescribeRepositories) must surface as-is — falling through to
  // create-repository turns it into a misleading CreateRepository AccessDenied.
  if (!/RepositoryNotFoundException/.test(r.stderr)) {
    throw new Error(`aws ecr describe-repositories --repository-names ${ecr} failed (exit ${r.code})\n${r.stderr}`);
  }
  await _run('aws', ['ecr', 'create-repository', '--repository-name', ecr, '--region', region, ...prof], { dryRun });
}
module.exports = { buildImage, pushImage, buildDeps, ecrLogin, ensureRepo, _withRun };
