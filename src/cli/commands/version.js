const { version } = require('../../../package.json');
module.exports = function registerVersion(program) {
  program.command('version').description('Show dray version').action(() => console.log(`dray ${version}`));
};
