const { removeRepo } = require('../../config/registry');
module.exports = function registerRemove(program) {
  program.command('remove <repo>').description('Unregister a repo').action((r) => { removeRepo(undefined, r); console.log(`removed ${r}`); });
};
