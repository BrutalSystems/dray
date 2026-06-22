const { allRepos } = require('../../config/registry'); const { publish } = require('../../primitives/pilet');
module.exports = function registerPublish(program) {
  program.command('publish <target>').description('Publish a pilet (repo:pilet)')
    .action(async (target, opts, cmd) => {
      const dryRun = cmd.optsWithGlobals().dryRun; const [repo, name] = target.split(':');
      const entry = allRepos()[repo]; if (!entry) throw new Error(`unknown repo "${repo}"`);
      const p = (entry.config.pilets || []).find((x) => x.name === name); if (!p) throw new Error(`no pilet "${name}" in ${repo}`);
      await publish({ repoPath: entry.path, secretsFile: p.secretsFile, publishCommand: p.publishCommand, dryRun });
    });
};
