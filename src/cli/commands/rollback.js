const { loadGlobalConfig } = require('../../config/global'); const { allRepos } = require('../../config/registry');
const { resolveTargets } = require('../../core/resolve'); const { renderManifests } = require('../../core/render'); const { applyFile } = require('../../primitives/kubectl');
module.exports = function registerRollback(program) {
  program.command('rollback <target> <sha>').description('Re-render + apply a prior SHA')
    .action(async (target, sha, opts, cmd) => {
      const dryRun = cmd.optsWithGlobals().dryRun; const { defaults } = loadGlobalConfig();
      const units = resolveTargets({ registry: allRepos(), globalDefaults: defaults, spec: target }).filter((u) => u.manifests.length);
      for (const u of units) {
        const vars = {}; for (const s of u.stamp) vars[s.var] = `${s.repoUri}:${sha}`;
        for (const f of renderManifests(u.manifests, vars, u.repoPath, { dryRun }))
          await applyFile({ file: f, context: u.defaults.context, namespace: u.defaults.namespace, dryRun });
        console.log(`rolled ${u.repo}:${u.workload || u.image.name} → ${sha}`);
      }
    });
};
