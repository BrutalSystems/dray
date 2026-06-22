const { Command } = require('commander');
async function run(argv) {
  const program = new Command();
  program.name('dray').description('Convention-driven multi-repo deploy orchestrator');
  require('./commands/version')(program);
  await program.parseAsync(argv);
}
module.exports = { run };
