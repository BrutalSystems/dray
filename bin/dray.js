#!/usr/bin/env node
require('../src/cli').run(process.argv).catch((e) => { console.error(e.message || e); process.exit(1); });
