const KEYS = ['profile', 'region', 'account', 'platform', 'context', 'namespace'];
const upperSnake = (s) => s.replace(/[^a-zA-Z0-9]+/g, '_').toUpperCase();
function imageVar(image) { return image.templateVar || `${upperSnake(image.name)}_IMAGE`; }
function mergeDefaults(globalDefaults = {}, repoCfg = {}, entry = {}) {
  const repo = repoCfg.defaults || {}; const out = {};
  for (const k of KEYS) out[k] = entry[k] ?? repo[k] ?? globalDefaults[k];
  return out;
}
function ecrRepoUri(defaults, image) { return `${defaults.account}.dkr.ecr.${defaults.region}.amazonaws.com/${image.ecr}`; }

function unitsForRepo(name, entry, globalDefaults, filter) {
  const cfg = entry.config;
  const images = new Map((cfg.images || []).map((i) => [i.name, i]));
  const workloads = cfg.workloads || [];
  const units = [];
  const mk = (image, w) => {
    const defaults = mergeDefaults(globalDefaults, cfg, w || {});
    const stampNames = (w && w.stampImages) || [image.name];
    const stamp = stampNames.map((n) => ({ var: imageVar(images.get(n)), repoUri: ecrRepoUri(defaults, images.get(n)) }));
    units.push({
      repo: name, repoPath: entry.path, image,
      workload: w ? w.name : null, kind: w ? w.kind : null,
      manifests: w ? w.manifests : [], dependsOn: (w && w.dependsOn) || [],
      defaults, repoUri: ecrRepoUri(defaults, image), stamp,
      _secrets: cfg.secrets || [],
    });
  };
  const want = (w) => !filter || w.name === filter || w.image === filter;
  for (const w of workloads) if (want(w)) mk(images.get(w.image), w);
  for (const img of images.values()) {
    const matches = !filter || img.name === filter;
    const hasWorkload = workloads.some((w) => w.image === img.name);
    if (matches && !hasWorkload) mk(img, null);
  }
  return units;
}
function resolveTargets({ registry, globalDefaults, spec }) {
  if (spec === '--all') return Object.entries(registry).flatMap(([n, e]) => unitsForRepo(n, e, globalDefaults, null));
  const [repo, name] = spec.split(':');
  const entry = registry[repo];
  if (!entry) throw new Error(`unknown repo "${repo}" (run: dray add)`);
  const units = unitsForRepo(repo, entry, globalDefaults, name || null);
  if (!units.length) throw new Error(`no target "${spec}" found`);
  return units;
}
module.exports = { mergeDefaults, imageVar, ecrRepoUri, resolveTargets };
