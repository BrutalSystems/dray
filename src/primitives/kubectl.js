let _run = require('./exec').run; function _withRun(fn) { _run = fn; }
const ns = (c, n) => ['-n', n, '--context', c];
function applyFile({ file, context, namespace, dryRun }) { return _run('kubectl', ['apply', '-f', file, ...ns(context, namespace)], { dryRun }); }
// `restart: false` waits without restarting. Used when the preceding apply
// stamped a new image SHA and therefore already started a rollout — issuing
// `rollout restart` on top of that produces a SECOND ReplicaSet and a second
// pod replacement, doubling the disruption window on every deploy.
// Default matches Kubernetes' own `progressDeadlineSeconds` (600s). Waiting
// LESS than the cluster does and then treating the timeout as a failure means
// giving up while Kubernetes still considers the rollout healthy -- which is
// what a cold node pulling a large image causes, routinely. Override per
// workload with `rolloutTimeoutSeconds` in .dray/config.json.
const DEFAULT_ROLLOUT_TIMEOUT_SECONDS = 600;

async function rollout({ deployment, context, namespace, dryRun, restart = true, timeoutSeconds }) {
  if (restart) await _run('kubectl', ['rollout', 'restart', `deployment/${deployment}`, ...ns(context, namespace)], { dryRun });
  const t = Number(timeoutSeconds) > 0 ? Number(timeoutSeconds) : DEFAULT_ROLLOUT_TIMEOUT_SECONDS;
  return _run('kubectl', ['rollout', 'status', `deployment/${deployment}`, `--timeout=${t}s`, ...ns(context, namespace)], { dryRun });
}

// Did KUBERNETES decide this rollout failed?
//
// `kubectl rollout status` exiting non-zero means "I stopped waiting", not
// "this is broken" -- and rolling back a healthy-but-slow deploy reverts
// working code and doubles the pod churn. The authoritative signal is the
// Progressing condition: ProgressDeadlineExceeded is the cluster giving up.
//
// Unreadable or unparseable -> false, i.e. do NOT roll back. Conservative in
// the direction that does no harm: a missed rollback leaves running code
// running, while a wrong one takes down something that was fine.
async function rolloutFailed({ deployment, context, namespace }) {
  const { code, stdout } = await _run('kubectl', ['get', `deployment/${deployment}`, '-o', 'json', ...ns(context, namespace)], { capture: true, allowFail: true });
  if (code !== 0) return false;
  let d;
  try { d = JSON.parse(stdout); } catch { return false; }
  const conds = (d.status && d.status.conditions) || [];
  return conds.some(c => c.type === 'Progressing' && c.reason === 'ProgressDeadlineExceeded');
}
function rolloutUndo({ deployment, context, namespace, dryRun }) { return _run('kubectl', ['rollout', 'undo', `deployment/${deployment}`, ...ns(context, namespace)], { dryRun }); }
// Is the Deployment controller ALREADY rolling out a change?
//
// Whether a rollout is under way is a property of the cluster, not of this process. An
// earlier design inferred it in the apply step and passed it to the rollout step through a
// field on the unit -- which works for `dray ship` but silently fails for `dray apply &&
// dray rollout`, two separate invocations with no shared memory. That is the shape every CI
// pipeline uses, so every deploy there rolled twice. Ask the cluster instead.
//
// Unreadable or absent deployment -> false, i.e. keep restarting. Conservative in the same
// direction as before: a missed restart does nothing at all, which is the worse failure.
async function rolloutInProgress({ deployment, context, namespace }) {
  const { code, stdout } = await _run('kubectl', ['get', `deployment/${deployment}`, '-o', 'json', ...ns(context, namespace)], { capture: true, allowFail: true });
  if (code !== 0) return false;
  let d;
  try { d = JSON.parse(stdout); } catch { return false; }
  const gen = d.metadata && d.metadata.generation;
  const obs = d.status && d.status.observedGeneration;
  if (typeof gen !== 'number') return false;
  // The controller has not yet observed the spec we just applied.
  if (gen !== obs) return true;
  const want = (d.spec && typeof d.spec.replicas === 'number') ? d.spec.replicas : 1;
  const updated = (d.status && d.status.updatedReplicas) || 0;
  const available = (d.status && d.status.availableReplicas) || 0;
  // Pods are still converging on the desired template.
  return updated < want || available < want;
}
async function runningImage({ workload, kind, context, namespace }) {
  const kindPath = kind === 'cronjob'
    ? ['cronjob/' + workload, '-o', 'jsonpath={.spec.jobTemplate.spec.template.spec.containers[0].image}']
    : ['deployment/' + workload, '-o', 'jsonpath={.spec.template.spec.containers[0].image}'];
  const { stdout } = await _run('kubectl', ['get', ...kindPath, ...ns(context, namespace)], { capture: true, allowFail: true });
  return stdout.trim();
}
module.exports = { applyFile, rollout, rolloutUndo, rolloutFailed, runningImage, rolloutInProgress, DEFAULT_ROLLOUT_TIMEOUT_SECONDS, _withRun };
