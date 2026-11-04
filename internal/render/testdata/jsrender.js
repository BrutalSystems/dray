// Renders every workload of one repo with the JS tool's own code, for the
// parity test in parity_test.go. Reads the registry without saving it.
//
//   node jsrender.js <js-tool-dir> <registry.json> <machine-config.json|-> <repo> <tag>
//
// Prints {"<workload>": {"vars": {...}, "files": [{"path", "text"}], "error"}}
// as JSON; error is set when a manifest could not be read.
const fs = require('node:fs');
const path = require('node:path');
const [toolDir, registryPath, machinePath, repo, tag] = process.argv.slice(2);
const { resolveTargets } = require(path.join(toolDir, 'src/core/resolve'));
const { renderString } = require(path.join(toolDir, 'src/core/render'));

delete process.env.GITHUB_ACTIONS;
const registry = JSON.parse(fs.readFileSync(registryPath, 'utf8'));
const machine = machinePath === '-' ? {} : JSON.parse(fs.readFileSync(machinePath, 'utf8'));
const globalDefaults = machine.defaults || {};
const entry = registry[repo];
const out = {};
for (const w of entry.config.workloads || []) {
  if (w.disabled) continue;
  const unit = resolveTargets({ registry, globalDefaults, spec: `${repo}:${w.name}` })
    .find((u) => u.workload === w.name);
  const vars = {};
  for (const s of unit.stamp) vars[s.var] = `${s.repoUri}:${tag}`;
  try {
    out[w.name] = {
      vars,
      files: unit.manifests.map((rel) => ({
        path: rel,
        text: renderString(fs.readFileSync(path.join(entry.path, rel), 'utf8'), vars),
      })),
    };
  } catch (err) {
    out[w.name] = { vars, error: err.code || String(err) };
  }
}
process.stdout.write(JSON.stringify(out));
