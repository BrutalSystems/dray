const { allRepos } = require('../../config/registry'); const { publish } = require('../../primitives/pilet');
module.exports = function registerPublish(program) {
  program.command('publish <target>').description('Publish pilet(s): "<repo>" for all, or "<repo>:<pilet>" for one')
    .action(async (target, opts, cmd) => {
      const dryRun = cmd.optsWithGlobals().dryRun;
      const [repo, name] = target.split(':');
      const entry = allRepos()[repo]; if (!entry) throw new Error(`unknown repo "${repo}"`);
      const pilets = entry.config.pilets;
      if (!pilets || !Array.isArray(pilets.names) || !pilets.names.length) {
        throw new Error(`no pilets configured in ${repo}`);
      }
      const names = name ? [name] : pilets.names;
      for (const n of names) {
        if (!pilets.names.includes(n)) throw new Error(`unknown pilet "${n}" in ${repo}`);
        const command = pilets.command.map((part) => part.split('{name}').join(n));
        await publish({ repoPath: entry.path, secretsFile: pilets.secretsFile, publishCommand: command, dryRun });
      }
    });
};
