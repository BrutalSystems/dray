let _run = require('./exec').run; function _withRun(fn) { _run = fn; }

// sops `exec-env` takes the command as a SINGLE string (run via the shell),
// not as `-- <argv>` (that form breaks on sops 3.x: "missing file to decrypt").
// Join + shell-quote so args like `typer[all]` survive.
function shQuote(a) {
  return /^[A-Za-z0-9_\-.,:=/@]+$/.test(a) ? a : `'${String(a).replace(/'/g, `'\\''`)}'`;
}
function execEnv(file, command, opts = {}) {
  return _run('sops', ['exec-env', file, command.map(shQuote).join(' ')], opts);
}
module.exports = { execEnv, _withRun };
