const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
const { run } = require('./exec');
async function currentSha(cwd) { const { stdout } = await run('git', ['rev-parse', '--short', 'HEAD'], { cwd, capture: true }); return stdout.trim(); }
async function isDirty(cwd) { const { stdout } = await run('git', ['status', '--porcelain'], { cwd, capture: true }); return stdout.trim().length > 0; }
async function cloneToTmp({ git, ref }) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-clone-'));
  // Works for branch/tag refs always; arbitrary commit SHAs require the remote
  // to allow SHA fetches (uploadpack.allowAnySHA1InWant).
  await run('git', ['init', '-q', dir], { capture: true });
  await run('git', ['-C', dir, 'remote', 'add', 'origin', git], { capture: true });
  await run('git', ['-C', dir, 'fetch', '--depth', '1', 'origin', ref], { capture: true });
  await run('git', ['-C', dir, 'checkout', '-q', 'FETCH_HEAD'], { capture: true });
  const sha = await currentSha(dir);
  return { dir, sha, cleanup: () => fs.rmSync(dir, { recursive: true, force: true }) };
}
module.exports = { currentSha, isDirty, cloneToTmp };
