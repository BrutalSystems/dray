let _run = require('./exec').run; function _withRun(fn) { _run = fn; }
function buildImage({ ecrUri, sha, dockerfile, context, platform, cwd, dryRun }) {
  return _run('docker', ['buildx', 'build', '--platform', platform, '-f', dockerfile, '-t', `${ecrUri}:${sha}`, '--load', context], { cwd, dryRun });
}
function pushImage({ ecrUri, sha, dryRun }) { return _run('docker', ['push', `${ecrUri}:${sha}`], { dryRun }); }
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
  if (r.code !== 0) await _run('aws', ['ecr', 'create-repository', '--repository-name', ecr, '--region', region, ...prof], { dryRun });
}
module.exports = { buildImage, pushImage, buildDeps, ecrLogin, ensureRepo, _withRun };
