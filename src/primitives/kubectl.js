let _run = require('./exec').run; function _withRun(fn) { _run = fn; }
const ns = (c, n) => ['-n', n, '--context', c];
function applyFile({ file, context, namespace, dryRun }) { return _run('kubectl', ['apply', '-f', file, ...ns(context, namespace)], { dryRun }); }
async function rollout({ deployment, context, namespace, dryRun }) {
  await _run('kubectl', ['rollout', 'restart', `deployment/${deployment}`, ...ns(context, namespace)], { dryRun });
  return _run('kubectl', ['rollout', 'status', `deployment/${deployment}`, '--timeout=180s', ...ns(context, namespace)], { dryRun });
}
function rolloutUndo({ deployment, context, namespace, dryRun }) { return _run('kubectl', ['rollout', 'undo', `deployment/${deployment}`, ...ns(context, namespace)], { dryRun }); }
async function runningImage({ workload, kind, context, namespace }) {
  const kindPath = kind === 'cronjob'
    ? ['cronjob/' + workload, '-o', 'jsonpath={.spec.jobTemplate.spec.template.spec.containers[0].image}']
    : ['deployment/' + workload, '-o', 'jsonpath={.spec.template.spec.containers[0].image}'];
  const { stdout } = await _run('kubectl', ['get', ...kindPath, ...ns(context, namespace)], { capture: true, allowFail: true });
  return stdout.trim();
}
module.exports = { applyFile, rollout, rolloutUndo, runningImage, _withRun };
