const { runAction } = require('../run-action');
module.exports = function registerApply(program) {
  program.command('apply [target]').description('Render + apply manifests')
    .option('--all', 'every registered repo')
    .action((target, opts, cmd) => runAction(target, 'apply', { ...cmd.optsWithGlobals(), all: opts.all }));
};
