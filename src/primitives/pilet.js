const sops = require('./sops');
function publish({ repoPath, secretsFile, publishCommand, dryRun }) { return sops.execEnv(secretsFile, publishCommand, { cwd: repoPath, dryRun }); }
module.exports = { publish };
