const fs = require('node:fs'); const os = require('node:os'); const path = require('node:path');
function renderString(text, vars) {
  let out = text;
  for (const [k, v] of Object.entries(vars)) out = out.split(`\${${k}}`).join(v);
  return out;
}
function renderManifests(files, vars, cwd, { dryRun } = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dray-render-'));
  return files.map((rel) => {
    const src = path.join(cwd, rel);
    const rendered = renderString(fs.readFileSync(src, 'utf8'), vars);
    const out = path.join(dir, rel.replace(/[/\\]/g, '__'));
    fs.writeFileSync(out, rendered);
    return out;
  });
}
module.exports = { renderString, renderManifests };
