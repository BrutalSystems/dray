const { allRepos } = require('../../config/registry');
module.exports = function registerList(program) {
  program.command('list').description('List repos + workloads').action(() => {
    for (const [n, e] of Object.entries(allRepos())) {
      const w = (e.config.workloads || []).map((x) => `${x.name}(${x.kind})`).join(', ') || '(none)';
      console.log(`${n}  [${w}]  ${e.path}`);
    }
  });
};
