const readline = require('node:readline'); const { allRepos } = require('../config/registry'); const { runAction } = require('./run-action');
function buildMenuTree(registry) {
  return {
    repos: Object.entries(registry).map(([name, e]) => ({ name, targets: (e.config.workloads || []).map((w) => ({ label: w.name, spec: `${name}:${w.name}` })) })),
    actions: ['ship', 'build', 'apply', 'rollout', 'status'],
  };
}
function defaultIo() { const rl = readline.createInterface({ input: process.stdin, output: process.stdout }); return { question: (q) => new Promise((r) => rl.question(q, r)), close: () => rl.close() }; }
async function menu(io = defaultIo()) {
  const t = buildMenuTree(allRepos());
  t.repos.forEach((r, i) => console.log(`${i + 1}) ${r.name}`));
  const repo = t.repos[parseInt(await io.question('repo #: '), 10) - 1]; if (!repo) return io.close();
  repo.targets.forEach((x, i) => console.log(`${i + 1}) ${x.label}`));
  const target = repo.targets[parseInt(await io.question('target #: '), 10) - 1];
  t.actions.forEach((a, i) => console.log(`${i + 1}) ${a}`));
  const action = t.actions[parseInt(await io.question('action #: '), 10) - 1];
  const ok = (await io.question(`${action} ${target.spec}? [y/N] `)).trim().toLowerCase() === 'y';
  io.close(); if (ok) await runAction(target.spec, action, { dryRun: false });
}
module.exports = { buildMenuTree, menu };
