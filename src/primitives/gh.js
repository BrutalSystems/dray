let _run = require('./exec').run; function _withRun(fn) { _run = fn; }

// Runs for the last `limit` workflow executions in this repo, newest first.
// One call for the whole repo rather than one per commit: the gate walks back
// through history looking for the newest CI-covered ancestor, and N lookups
// would be N round trips to the API.
async function recentRuns(cwd, limit = 100) {
  let code, stdout, stderr;
  try {
    ({ code, stdout, stderr } = await _run('gh',
      ['run', 'list', '--limit', String(limit), '--json', 'headSha,status,conclusion,workflowName,url'],
      { cwd, capture: true, allowFail: true }));
  } catch (err) {
    // spawn() itself failed -- with no `gh` on PATH the process never starts, so
    // there is no exit code or stderr to inspect below.
    if (/ENOENT/i.test(err.message)) throw new Error('gh CLI not installed (brew install gh)');
    throw err;
  }
  if (code !== 0) {
    const msg = (stderr || '').trim();
    // Distinguish "gh cannot answer" from "gh says no". A missing binary, an
    // expired token or a repo with Actions disabled must not read as a green
    // suite -- the caller turns this into a block, not a pass.
    if (/not found|ENOENT/i.test(msg)) throw new Error('gh CLI not installed (brew install gh)');
    if (/auth|login|token/i.test(msg)) throw new Error('gh CLI not authenticated (gh auth login)');
    throw new Error(`gh run list failed: ${msg || `exit ${code}`}`);
  }
  try { return JSON.parse(stdout || '[]'); } catch { throw new Error('gh run list returned unparseable JSON'); }
}

// Commit shas from HEAD backwards, newest first.
async function recentCommits(cwd, limit = 50) {
  const { stdout } = await _run('git', ['rev-list', '-n', String(limit), 'HEAD'], { cwd, capture: true });
  return stdout.trim().split('\n').filter(Boolean);
}

module.exports = { recentRuns, recentCommits, _withRun };
