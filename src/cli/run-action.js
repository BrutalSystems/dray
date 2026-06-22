const { loadGlobalConfig } = require('../config/global'); const { allRepos } = require('../config/registry');
const { resolveTargets } = require('../core/resolve'); const { planFor } = require('../core/plan'); const { execute } = require('../core/engine');
async function runAction(target, action, { dryRun, allowDirty, all }) {
  const registry = allRepos(); const { defaults } = loadGlobalConfig();
  const spec = all ? '--all' : target;
  const units = resolveTargets({ registry, globalDefaults: defaults, spec });
  await execute(planFor(units, action), { dryRun, allowDirty });
}
module.exports = { runAction };
