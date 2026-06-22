let _run = require('./exec').run; function _withRun(fn) { _run = fn; }
async function syncSecret(secret, { repoPath, context, namespace, profile, dryRun }) {
  if (secret.kind === 'sops-exec-env') return; // handled by pilet publish
  if (secret.kind === 'sops-manifest') {
    return _run('bash', ['-c', `sops -d ${secret.file} | kubectl apply -f - -n ${namespace} --context ${context}`], { cwd: repoPath, dryRun });
  }
  if (secret.kind === 'literal-from-env') {
    await _run('bash', ['-c',
      `kubectl create secret generic ${secret.name} --from-env-file=${secret.file} --dry-run=client -o yaml -n ${namespace} --context ${context} | kubectl apply -f - -n ${namespace} --context ${context}`],
      { cwd: repoPath, dryRun });
    for (const [k, v] of Object.entries(secret.reflector || {})) {
      await _run('kubectl', ['annotate', 'secret', secret.name, `${k}=${v}`, '--overwrite', '-n', namespace, '--context', context], { dryRun });
    }
  }
}
module.exports = { syncSecret, _withRun };
