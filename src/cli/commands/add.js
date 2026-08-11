const fs = require('node:fs'); const path = require('node:path');
const { loadRepoConfig } = require('../../config/repo'); const { addRepo } = require('../../config/registry');

// The registry is consulted from arbitrary working directories, so a stored path
// MUST be absolute. `dray add .` used to store "." verbatim, which then resolved
// against whatever cwd dray ran in later -- two repos added that way both pointed
// at "the current directory", and `dray ship <a>` from repo b deployed b under a's
// name. realpath so symlinked checkouts land on one canonical path.
function resolveRepoPath(repoPath) {
  const p = path.resolve(repoPath || process.cwd());
  try { return fs.realpathSync(p); } catch { return p; }
}

module.exports = function registerAdd(program) {
  program.command('add [repoPath]').description('Register a repo').action((repoPath) => {
    const p = resolveRepoPath(repoPath); const config = loadRepoConfig(p);
    addRepo(undefined, { name: config.name, path: p, config }); console.log(`registered ${config.name} → ${p}`);
  });
};
module.exports.resolveRepoPath = resolveRepoPath;
