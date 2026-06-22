const { loadGlobalConfig } = require('../../config/global'); const { allRepos } = require('../../config/registry');
const { resolveTargets } = require('../../core/resolve'); const { runningImage } = require('../../primitives/kubectl'); const { currentSha } = require('../../primitives/git');
module.exports = function registerStatus(program) {
  program.command('status [target]').description('Running image SHA vs HEAD').option('--all', 'all repos')
    .action(async (target, opts) => {
      const { defaults } = loadGlobalConfig();
      const units = resolveTargets({ registry: allRepos(), globalDefaults: defaults, spec: opts.all || !target ? '--all' : target }).filter((u) => u.workload);
      for (const u of units) {
        const live = await runningImage({ workload: u.workload, kind: u.kind, context: u.defaults.context, namespace: u.defaults.namespace });
        const head = await currentSha(u.repoPath).catch(() => '?');
        const tag = live.includes(':') ? live.split(':').pop() : '(none)';
        console.log(`${u.repo}:${u.workload}  running ${tag}  ${tag === head ? 'up-to-date' : `behind (HEAD ${head})`}`);
      }
    });
};
