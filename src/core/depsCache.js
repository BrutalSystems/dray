const fs = require('node:fs'); const path = require('node:path'); const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');
const { DRAY_HOME } = require('../constants');

// Whether the deps base image tag exists in the local Docker image store. A
// `docker system prune` can evict it while the lockfiles are unchanged — the hash
// check alone would then skip the rebuild and the main build fails at
// `FROM <depsImage.tag>: not found`.
function depsImagePresent(tag) {
  try { execFileSync('docker', ['image', 'inspect', tag], { stdio: 'ignore' }); return true; }
  catch { return false; }
}

// `imagePresent` is injectable for tests; defaults to the real docker check.
function needsDepsRebuild(image, repoPath, repo, stateDir = path.join(DRAY_HOME, 'depshash'), imagePresent = depsImagePresent) {
  if (!image.depsImage) return false;
  const h = crypto.createHash('sha256');
  for (const rel of image.depsImage.rebuildOn || []) {
    const p = path.join(repoPath, rel); h.update(rel);
    h.update(fs.existsSync(p) ? fs.readFileSync(p) : Buffer.from('MISSING'));
  }
  const digest = h.digest('hex');
  fs.mkdirSync(stateDir, { recursive: true });
  const file = path.join(stateDir, `${repo}__${image.name}.depshash`);
  const prev = fs.existsSync(file) ? fs.readFileSync(file, 'utf8') : null;
  // Rebuild when the lockfiles changed OR the cached deps image tag is gone.
  const present = !image.depsImage.tag || imagePresent(image.depsImage.tag);
  if (prev === digest && present) return false;
  fs.writeFileSync(file, digest); return true;
}
module.exports = { needsDepsRebuild, depsImagePresent };
