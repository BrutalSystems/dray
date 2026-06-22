function uniqueImages(units) {
  const seen = new Map();
  for (const u of units) { const key = `${u.repo}/${u.image.name}`; if (!seen.has(key)) seen.set(key, u); }
  return [...seen.values()];
}
const buildSteps = (u) => {
  const s = [];
  if (u.image.depsImage) s.push({ kind: 'deps', label: `deps ${u.image.name}`, unit: u });
  s.push({ kind: 'build', label: `build ${u.image.name}`, unit: u });
  return s;
};
const withManifests = (units) => units.filter((u) => u.manifests && u.manifests.length);
const deployments = (units) => units.filter((u) => u.kind === 'deployment');
function topoSort(units) {
  const byName = new Map(units.map((u) => [u.workload, u]));
  const out = []; const seen = new Set();
  const visit = (u) => {
    if (!u || seen.has(u.workload)) return; seen.add(u.workload);
    for (const dep of u.dependsOn || []) if (!dep.startsWith('secret:')) visit(byName.get(dep));
    out.push(u);
  };
  for (const u of units) visit(u);
  return out;
}
function secretSteps(units) {
  // Repo-scoped dedup (a secret named "mongodb-app" in two repos is two
  // distinct steps). Each step carries the fully-resolved secret object from
  // its OWNING unit — no fragile name lookup / silent fallback in the engine.
  const out = []; const seen = new Set();
  for (const u of units) {
    for (const d of u.dependsOn || []) {
      if (!d.startsWith('secret:')) continue;
      const name = d.slice('secret:'.length);
      const key = `${u.repo}/${name}`;
      if (seen.has(key)) continue;
      seen.add(key);
      const secret = (u._secrets || []).find((s) => s.name === name);
      if (!secret) throw new Error(`${u.repo}: workload "${u.workload}" dependsOn secret "${name}" not declared in secrets[]`);
      out.push({ kind: 'secret', label: `secret ${u.repo}:${name}`, secret, unit: u });
    }
  }
  return out;
}
function planFor(units, action) {
  const imgs = uniqueImages(units);
  if (action === 'build') return imgs.flatMap(buildSteps);
  if (action === 'push') return imgs.flatMap((u) => [...buildSteps(u), { kind: 'push', label: `push ${u.image.name}`, unit: u }]);
  if (action === 'apply') return withManifests(units).map((u) => ({ kind: 'apply', label: `apply ${u.workload || u.image.name}`, unit: u }));
  if (action === 'rollout') return deployments(units).map((u) => ({ kind: 'rollout', label: `rollout ${u.workload}`, unit: u }));
  if (action === 'ship') {
    const ordered = topoSort(units);
    return [
      ...imgs.flatMap((u) => [...buildSteps(u), { kind: 'push', label: `push ${u.image.name}`, unit: u }]),
      ...secretSteps(ordered),
      ...withManifests(ordered).map((u) => ({ kind: 'apply', label: `apply ${u.workload || u.image.name}`, unit: u })),
      ...deployments(ordered).map((u) => ({ kind: 'rollout', label: `rollout ${u.workload}`, unit: u })),
    ];
  }
  throw new Error(`unknown action "${action}"`);
}
module.exports = { planFor };
