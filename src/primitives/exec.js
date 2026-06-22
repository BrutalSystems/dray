const { spawn } = require('node:child_process');
function run(cmd, args = [], opts = {}) {
  if (opts.dryRun) {
    console.log(`▶ ${cmd} ${args.join(' ')}${opts.cwd ? ` (cwd: ${opts.cwd})` : ''}`);
    return Promise.resolve({ code: 0, stdout: '', stderr: '' });
  }
  return new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { cwd: opts.cwd, stdio: opts.capture ? ['inherit', 'pipe', 'pipe'] : 'inherit' });
    let stdout = '', stderr = '';
    if (opts.capture) { child.stdout.on('data', (d) => { stdout += d; }); child.stderr.on('data', (d) => { stderr += d; }); }
    child.on('error', reject);
    child.on('close', (code) => {
      if (code !== 0 && !opts.allowFail) reject(new Error(`${cmd} ${args.join(' ')} failed (exit ${code})\n${stderr}`));
      else resolve({ code, stdout, stderr });
    });
  });
}
module.exports = { run };
