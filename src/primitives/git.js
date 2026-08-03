const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
const { run } = require('./exec');
async function currentSha(cwd) { const { stdout } = await run('git', ['rev-parse', '--short', 'HEAD'], { cwd, capture: true }); return stdout.trim(); }
async function isDirty(cwd) { const { stdout } = await run('git', ['status', '--porcelain'], { cwd, capture: true }); return stdout.trim().length > 0; }
// The HEAD sha of a git-source image's REMOTE ref, without cloning — so `status`
// compares against the actual build source, not the local repo it happens to be
// invoked from. Short form to match `currentSha`.
async function lsRemote({ git, ref }) { const { stdout } = await run('git', ['ls-remote', git, ref], { capture: true }); return (stdout.trim().split(/\s+/)[0] || '').slice(0, 7); }
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
module.exports = { currentSha, isDirty, cloneToTmp, lsRemote };
