const { runAction } = require('../run-action');
module.exports = function registerPush(program) {
  program.command('push [target]').description('Build + push image(s)')
    .option('--all', 'every registered repo')
    .action((target, opts, cmd) => runAction(target, 'push', { ...cmd.optsWithGlobals(), all: opts.all }));
};
