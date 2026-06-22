const os = require('node:os'); const path = require('node:path');
const DRAY_HOME = path.join(os.homedir(), '.dray');
module.exports = { DRAY_HOME, GLOBAL_CONFIG: path.join(DRAY_HOME, 'config.json'), REGISTRY: path.join(DRAY_HOME, 'registry.json') };
