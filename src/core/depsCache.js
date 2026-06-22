const fs = require('node:fs'); const path = require('node:path'); const crypto = require('node:crypto');
const { DRAY_HOME } = require('../constants');
function needsDepsRebuild(image, repoPath, repo, stateDir = path.join(DRAY_HOME, 'depshash')) {
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
  if (prev === digest) return false;
  fs.writeFileSync(file, digest); return true;
}
module.exports = { needsDepsRebuild };
