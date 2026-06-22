const { Command } = require('commander');
async function run(argv) {
  if (argv.length <= 2) { await require('./menu').menu(); return; }
  const program = new Command();
  program.name('dray').description('Convention-driven multi-repo deploy orchestrator');
  program.option('--dry-run', 'print planned commands, run nothing');
  program.option('--allow-dirty', 'allow building from a dirty tree (tags :<sha>-dirty)');
  for (const c of ['version', 'init', 'add', 'remove', 'reload', 'list',
    'build', 'push', 'apply', 'rollout', 'ship', 'status', 'rollback', 'publish', 'secrets']) {
    require(`./commands/${c}`)(program);
  }
  await program.parseAsync(argv);
}
module.exports = { run };
