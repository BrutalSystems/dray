const { runAction } = require('../run-action');
module.exports = function registerBuild(program) {
  program.command('build [target]').description('Build image(s)')
    .option('--all', 'every registered repo')
    .action((target, opts, cmd) => runAction(target, 'build', { ...cmd.optsWithGlobals(), all: opts.all }));
};
