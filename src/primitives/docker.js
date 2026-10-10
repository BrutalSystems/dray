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
//
// A commit that is ALREADY in ECR is never re-pushed (2026-10-10). Re-shipping a
// commit rebuilds it, and pushing that rebuild silently changed what the tag meant
// (bs-jobs-service:642d56c pointed at a September rebuild while the running image's
// digest had been deleted). With ECR tag immutability such a push is rejected anyway.
// So: keep the image ECR has — it is the one that was tested and deployed — and point
// :latest at THAT image (server-side, by manifest), not at the local rebuild.
// Skipped when the repo name isn't known (callers without ECR details), for dirty
// builds (always pushed; excluded from immutability), and on dry runs.
async function pushImage({ ecrUri, sha, latest = false, dryRun, ecr, region, profile }) {
  const dirty = String(sha).endsWith('-dirty');
  const ecrCtx = ecr && region ? { ecr, region, profile } : null;
  if (ecrCtx && !dirty && !dryRun) {
    if (await _tagExists(ecrCtx, sha)) {
      console.log(`${ecrUri}:${sha} already in ECR — not re-pushing; deploying the existing image`);
      if (latest) await _retagInEcr(ecrCtx, sha, 'latest');
      return;
    }
    try {
      await _run('docker', ['push', `${ecrUri}:${sha}`], { dryRun });
    } catch (e) {
      // Raced with another push of the same commit into an immutable repo.
      if (/already exists .*immutable|ImageTagAlreadyExistsException/i.test(String(e.message)) && await _tagExists(ecrCtx, sha)) {
        console.log(`${ecrUri}:${sha} was pushed concurrently — keeping the image ECR has`);
        if (latest) await _retagInEcr(ecrCtx, sha, 'latest');
        return;
      }
      throw e;
    }
  } else {
    await _run('docker', ['push', `${ecrUri}:${sha}`], { dryRun });
  }
  if (latest && !dirty) {
    await _run('docker', ['tag', `${ecrUri}:${sha}`, `${ecrUri}:latest`], { dryRun });
    await _run('docker', ['push', `${ecrUri}:latest`], { dryRun });
  }
}
function _profArgs(profile) { return profile ? ['--profile', profile] : []; }
async function _tagExists({ ecr, region, profile }, tag) {
  const r = await _run('aws', ['ecr', 'describe-images', '--repository-name', ecr, '--image-ids', `imageTag=${tag}`, '--region', region, ..._profArgs(profile)],
    { capture: true, allowFail: true });
  if (r.code === 0) return true;
  if (/ImageNotFoundException/.test(r.stderr)) return false;
  // Anything else (no ecr:DescribeImages, network) must not be mistaken for "missing":
  // pushing blind is exactly what this check exists to prevent.
  throw new Error(`aws ecr describe-images --repository-name ${ecr} --image-ids imageTag=${tag} failed (exit ${r.code})\n${r.stderr}`);
}
async function _retagInEcr({ ecr, region, profile }, fromTag, toTag) {
  const g = await _run('aws', ['ecr', 'batch-get-image', '--repository-name', ecr, '--image-ids', `imageTag=${fromTag}`, '--region', region,
    '--query', 'images[0].imageManifest', '--output', 'text', ..._profArgs(profile)], { capture: true });
  const manifest = String(g.stdout).trim();
  const r = await _run('aws', ['ecr', 'put-image', '--repository-name', ecr, '--image-tag', toTag, '--image-manifest', manifest, '--region', region, ..._profArgs(profile)],
    { capture: true, allowFail: true });
  if (r.code !== 0 && !/ImageAlreadyExistsException/.test(r.stderr)) {
    throw new Error(`aws ecr put-image --image-tag ${toTag} (from ${fromTag}) failed (exit ${r.code})\n${r.stderr}`);
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
