const { runAction } = require('../run-action');
module.exports = function registerRollout(program) {
  program.command('rollout [target]').description('Restart + wait deployments')
    .option('--all', 'every registered repo')
    .action((target, opts, cmd) => runAction(target, 'rollout', { ...cmd.optsWithGlobals(), all: opts.all }));
};
