let _run = require('./exec').run; function _withRun(fn) { _run = fn; }
function execEnv(file, command, opts = {}) { return _run('sops', ['exec-env', file, '--', ...command], opts); }
module.exports = { execEnv, _withRun };
