const { loadGlobalConfig } = require('../../config/global'); const { allRepos } = require('../../config/registry');
const { mergeDefaults } = require('../../core/resolve'); const { syncSecret } = require('../../primitives/secrets');
module.exports = function registerSecrets(program) {
  const grp = program.command('secrets').description('Secrets operations');
  grp.command('sync <repo> [name]').description('Sync declared secrets')
    .action(async (repo, name, opts, c) => {
      const dryRun = c.optsWithGlobals().dryRun; const entry = allRepos()[repo]; if (!entry) throw new Error(`unknown repo "${repo}"`);
      const { defaults } = loadGlobalConfig(); const d = mergeDefaults(defaults, entry.config, {});
      for (const s of (entry.config.secrets || []).filter((s) => !name || s.name === name)) {
        await syncSecret(s, { repoPath: entry.path, context: d.context, namespace: d.namespace, profile: d.profile, dryRun });
        console.log(`synced ${s.name}`);
      }
    });
};
