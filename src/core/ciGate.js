const real = { gh: require('../primitives/gh'), git: require('../primitives/git') };

const PASSING = new Set(['success', 'skipped', 'neutral']);

// Pure decision: given HEAD's ancestry (newest first) and the repo's recent
// workflow runs, may we deploy?
//
// Why walk back through history instead of demanding a run for HEAD itself:
// workflows commonly use paths-ignore, so a docs-only commit produces NO run
// at all -- not a skipped one. Requiring a run for HEAD would refuse to ship a
// README change, and a gate that blocks legitimate work is a gate that gets
// switched off. Instead we require the newest CI-COVERED ancestor to be green:
// if the commits after it were deemed unable to affect the artifact, the last
// verdict still stands.
function evaluate({ commits, runs }) {
  const byShaPrefix = new Map();
  for (const r of runs) {
    if (!r.headSha) continue;
    const list = byShaPrefix.get(r.headSha) || [];
    list.push(r);
    byShaPrefix.set(r.headSha, list);
  }
  for (const sha of commits) {
    const hits = byShaPrefix.get(sha);
    if (!hits || !hits.length) continue; // not CI-covered; keep walking back
    const short = sha.slice(0, 7);
    const pending = hits.filter((r) => r.status !== 'completed');
    if (pending.length) {
      return { ok: false, sha: short, reason: 'pending',
        detail: `CI is still running for ${short} (${pending.map((r) => r.workflowName).join(', ')}). Wait for it to finish.`,
        url: pending[0].url };
    }
    const failed = hits.filter((r) => !PASSING.has(r.conclusion));
    if (failed.length) {
      return { ok: false, sha: short, reason: 'failed',
        detail: `CI failed for ${short}: ${failed.map((r) => `${r.workflowName}=${r.conclusion}`).join(', ')}`,
        url: failed[0].url };
    }
    return { ok: true, sha: short, reason: 'green',
      detail: `CI green for ${short} (${hits.map((r) => r.workflowName).join(', ')})` };
  }
  return { ok: false, reason: 'absent',
    detail: `no CI run found for HEAD or any of its ${commits.length} most recent ancestors. `
      + 'Push the branch and let CI run, or override.' };
}

// Repos to gate, deduped by path. Images sourced from a remote git ref are
// skipped: their code lives in another repository and its CI is not this
// repo's to read. That is a real hole -- see README -- not an oversight.
function gatedRepos(units) {
  const out = new Map();
  for (const u of units) {
    if (!u.defaults || !u.defaults.ciGate) continue;
    if (u.image && u.image.source && u.image.source.git) continue;
    if (!out.has(u.repoPath)) out.set(u.repoPath, u.repo);
  }
  return out;
}

async function assertCiGreen(units, { dryRun = false, allowDirty = false, skipCiCheck = false, deps, log = console.error } = {}) {
  const d = { ...real, ...deps };
  const repos = gatedRepos(units);
  if (!repos.size) return;
  if (dryRun) return;
  // Running inside GitHub Actions, the gate would be checking the very run that
  // invoked it -- always "pending", a guaranteed deadlock. CI reaching dray at
  // all means the pipeline already decided to deploy.
  if (process.env.GITHUB_ACTIONS === 'true') return;
  if (skipCiCheck) {
    for (const [, name] of repos) log(`!! CI gate SKIPPED for ${name} (--skip-ci-check) -- deploying unverified code`);
    return;
  }
  for (const [repoPath, name] of repos) {
    if (allowDirty && await d.git.isDirty(repoPath)) {
      throw new Error(`${name}: working tree is dirty, so no CI run can exist for what you are about to ship.\n`
        + '  Commit and push it, or pass --skip-ci-check to override.');
    }
    let commits, runs;
    try {
      [commits, runs] = await Promise.all([d.gh.recentCommits(repoPath), d.gh.recentRuns(repoPath)]);
    } catch (err) {
      // A gh that cannot answer is not a green suite. Say it in the gate's own
      // voice: a bare "gh CLI not authenticated" reads like an unrelated tool
      // failure rather than like the deploy having been stopped on purpose.
      throw new Error(`${name}: CI gate could not check CI, so it is refusing to deploy.\n  ${err.message}`
        + '\n  Fix that, or pass --skip-ci-check to deploy without the check.');
    }
    const verdict = evaluate({ commits, runs });
    if (!verdict.ok) {
      throw new Error(`${name}: CI gate blocked this deploy.\n  ${verdict.detail}`
        + (verdict.url ? `\n  ${verdict.url}` : '')
        + '\n  Override with --skip-ci-check if you must ship anyway.');
    }
    log(`✓ ${name}: ${verdict.detail}`);
  }
}

module.exports = { evaluate, gatedRepos, assertCiGreen };
