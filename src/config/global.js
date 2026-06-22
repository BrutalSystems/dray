const fs = require('node:fs'); const { GLOBAL_CONFIG } = require('../constants');
function parseGlobalConfig(raw) { if (!raw) return { defaults: {} }; const o = JSON.parse(raw); return { defaults: o.defaults || {} }; }
function loadGlobalConfig() { return parseGlobalConfig(fs.existsSync(GLOBAL_CONFIG) ? fs.readFileSync(GLOBAL_CONFIG, 'utf8') : null); }
module.exports = { parseGlobalConfig, loadGlobalConfig };
