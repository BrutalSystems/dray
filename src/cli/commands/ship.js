const { runAction } = require('../run-action');
module.exports = function registerShip(program) {
  program.command('ship [target]').description('Build + push + apply + rollout a target')
    .option('--all', 'every registered repo')
    .action((target, opts, cmd) => runAction(target, 'ship', { ...cmd.optsWithGlobals(), all: opts.all }));
};
