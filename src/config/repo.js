const fs = require('node:fs'); const path = require('node:path');
const WORKLOAD_KINDS = new Set(['deployment', 'cronjob']);
const SECRET_KINDS = new Set(['sops-manifest', 'literal-from-env', 'sops-exec-env']);
function loadRepoConfig(repoPath) {
  const file = path.join(repoPath, '.dray', 'config.json');
  if (!fs.existsSync(file)) throw new Error(`no .dray/config.json in ${repoPath}`);
  return validateRepoConfig(JSON.parse(fs.readFileSync(file, 'utf8')));
}
function validateRepoConfig(cfg) {
  const e = [];
  if (!cfg.name || typeof cfg.name !== 'string') e.push('name must be a non-empty string');
  const imageNames = new Set();
  for (const img of cfg.images || []) {
    if (!img.name) e.push('image missing name');
    if (!img.ecr) e.push(`image "${img.name}" missing ecr`);
    if (!(img.source && (img.source.local || img.source.git))) e.push(`image "${img.name}" needs source.local or source.git`);
    imageNames.add(img.name);
  }
  for (const w of cfg.workloads || []) {
    if (!w.name) e.push('workload missing name');
    if (!WORKLOAD_KINDS.has(w.kind)) e.push(`workload "${w.name}" kind must be deployment|cronjob`);
    if (w.image !== undefined && !imageNames.has(w.image)) e.push(`workload "${w.name}" references unknown image "${w.image}"`);
    if (!Array.isArray(w.manifests) || w.manifests.length === 0) e.push(`workload "${w.name}" needs a non-empty manifests list`);
  }
  for (const s of cfg.secrets || []) {
    if (!s.name) e.push('secret missing name');
    if (!SECRET_KINDS.has(s.kind)) e.push(`secret "${s.name}" kind must be sops-manifest|literal-from-env|sops-exec-env`);
  }
  if (e.length) throw new Error(`invalid .dray/config.json:\n  - ${e.join('\n  - ')}`);
  return cfg;
}
module.exports = { loadRepoConfig, validateRepoConfig };
