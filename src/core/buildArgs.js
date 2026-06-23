const fs = require('node:fs'); const path = require('node:path');

function stripQuotes(v) {
  const t = v.trim();
  if ((t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'"))) {
    return t.slice(1, -1);
  }
  return t;
}

// Returns ["KEY=VALUE", ...] for `docker build --build-arg`. An image may set
// `buildArgs: { envFile, prefix }` to inject vars from an env file (e.g. VITE_*
// from .env.production), and/or `buildArgs.values: { K: V }` for explicit pairs.
function resolveBuildArgs(image, cwd) {
  const cfg = image && image.buildArgs;
  if (!cfg) return [];
  const out = [];
  if (cfg.envFile) {
    const p = path.join(cwd, cfg.envFile);
    if (fs.existsSync(p)) {
      for (const line of fs.readFileSync(p, 'utf8').split('\n')) {
        const m = line.match(/^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/);
        if (!m) continue;
        const [, k, v] = m;
        if (cfg.prefix && !k.startsWith(cfg.prefix)) continue;
        out.push(`${k}=${stripQuotes(v)}`);
      }
    }
  }
  for (const [k, v] of Object.entries(cfg.values || {})) out.push(`${k}=${v}`);
  return out;
}

module.exports = { resolveBuildArgs };
