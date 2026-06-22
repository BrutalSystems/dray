const { loadRepoConfig } = require('../../config/repo'); const { addRepo } = require('../../config/registry');
module.exports = function registerAdd(program) {
  program.command('add [repoPath]').description('Register a repo').action((repoPath) => {
    const p = repoPath || process.cwd(); const config = loadRepoConfig(p);
    addRepo(undefined, { name: config.name, path: p, config }); console.log(`registered ${config.name} → ${p}`);
  });
};
