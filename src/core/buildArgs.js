const fs = require('node:fs'); const path = require('node:path');
let _run = require('../primitives/exec').run; function _withRun(fn) { _run = fn; }

function stripQuotes(v) {
  const t = v.trim();
  if ((t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'"))) {
    return t.slice(1, -1);
  }
  return t;
}

// Parse dotenv-style text into ["KEY=VALUE", ...], filtered by `prefix`.
function parseEnvLines(text, prefix) {
  const out = [];
  for (const line of text.split('\n')) {
    const m = line.match(/^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/);
    if (!m) continue;
    const [, k, v] = m;
    if (prefix && !k.startsWith(prefix)) continue;
    out.push(`${k}=${stripQuotes(v)}`);
  }
  return out;
}

// Returns ["KEY=VALUE", ...] for `docker build --build-arg`. An image may set
// `buildArgs: { envFile, prefix }` to inject vars from a plaintext env file
// (e.g. VITE_* from .env.production), `buildArgs: { sopsEnvFile, prefix }` to
// inject them from a sops-encrypted file decrypted in memory at build time (no
// plaintext on disk), and/or `buildArgs.values: { K: V }` for explicit pairs.
async function resolveBuildArgs(image, cwd, { dryRun = false } = {}) {
  const cfg = image && image.buildArgs;
  if (!cfg) return [];
  const out = [];
  if (cfg.envFile) {
    const p = path.join(cwd, cfg.envFile);
    if (fs.existsSync(p)) out.push(...parseEnvLines(fs.readFileSync(p, 'utf8'), cfg.prefix));
  }
  if (cfg.sopsEnvFile) {
    const { stdout } = await _run('sops', ['-d', '--output-type', 'dotenv', cfg.sopsEnvFile], { cwd, capture: true, dryRun });
    out.push(...parseEnvLines(stdout || '', cfg.prefix));
  }
  for (const [k, v] of Object.entries(cfg.values || {})) out.push(`${k}=${v}`);
  return out;
}

module.exports = { resolveBuildArgs, _withRun };
