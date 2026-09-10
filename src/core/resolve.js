const KEYS = ['profile', 'region', 'account', 'platform', 'context', 'namespace', 'ciGate'];
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
    // A workload may have no image (apply-only, e.g. a third-party image that
    // lives literally in the manifest) — then there's nothing to build or stamp.
    const stampNames = (w && w.stampImages) || (image ? [image.name] : []);
    const stamp = stampNames.map((n) => ({ var: imageVar(images.get(n)), repoUri: ecrRepoUri(defaults, images.get(n)) }));
    units.push({
      repo: name, repoPath: entry.path, image: image || null,
      workload: w ? w.name : null, kind: w ? w.kind : null,
      manifests: w ? w.manifests : [], dependsOn: (w && w.dependsOn) || [],
      // Per-workload override for how long to wait on `rollout status`.
      // Undefined falls back to kubectl.DEFAULT_ROLLOUT_TIMEOUT_SECONDS, which
      // matches Kubernetes' own progressDeadlineSeconds. Raise it for workloads
      // with large images that land on cold nodes.
      rolloutTimeoutSeconds: (w && w.rolloutTimeoutSeconds) || undefined,
      defaults, repoUri: image ? ecrRepoUri(defaults, image) : null, stamp,
      _secrets: cfg.secrets || [],
    });
  };
  const want = (w) => !filter || w.name === filter || w.image === filter;
  for (const w of workloads) {
    if (!want(w)) continue;
    if (w.disabled) {
      // Documented in config but not deployed by dray (e.g. endpoints served
      // elsewhere). Skip in bare-repo/--all; error if explicitly targeted by name.
      if (filter && w.name === filter) {
        throw new Error(`workload "${w.name}" is disabled in ${name}'s .dray/config.json`);
      }
      continue;
    }
    // Deployed only when explicitly targeted (dray ship repo:name). Skipped by a
    // bare-repo ship and --all -- that is what CI runs, so `manual` keeps a
    // workload out of the automatic pipeline while still allowing an on-demand
    // deploy. Unlike `disabled`, targeting it by name is not an error. The image
    // is skipped with it: the image-only fallback below only fires when NO
    // workload claims the image, and a skipped manual workload still claims it.
    if (w.manual && !filter) continue;
    mk(images.get(w.image), w);
  }
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
