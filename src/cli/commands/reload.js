const { loadRepoConfig } = require('../../config/repo'); const { allRepos, addRepo } = require('../../config/registry');
module.exports = function registerReload(program) {
  program.command('reload [repo]').description('Re-read config').action((repo) => {
    const reg = allRepos(); for (const n of (repo ? [repo] : Object.keys(reg))) {
      addRepo(undefined, { name: n, path: reg[n].path, config: loadRepoConfig(reg[n].path) }); console.log(`reloaded ${n}`);
    }
  });
};
