const { loadGlobalConfig } = require('../config/global'); const { allRepos } = require('../config/registry');
const { resolveTargets } = require('../core/resolve'); const { planFor } = require('../core/plan'); const { execute } = require('../core/engine');
const { assertCiGreen } = require('../core/ciGate');
// Actions that publish an artifact or touch the cluster. `build` is local and
// leaves nothing behind, so it stays ungated.
const GATED = new Set(['push', 'apply', 'rollout', 'ship']);
async function runAction(target, action, { dryRun, allowDirty, all, skipCiCheck }) {
  const registry = allRepos(); const { defaults } = loadGlobalConfig();
  const spec = all ? '--all' : target;
  const units = resolveTargets({ registry, globalDefaults: defaults, spec });
  if (GATED.has(action)) await assertCiGreen(units, { dryRun, allowDirty, skipCiCheck });
  await execute(planFor(units, action), { dryRun, allowDirty });
}
module.exports = { runAction };
