const fs = require('node:fs'); const path = require('node:path');
module.exports = function registerInit(program) {
  program.command('init').description('Scaffold .dray/config.json').action(() => {
    const dir = path.join(process.cwd(), '.dray'); fs.mkdirSync(dir, { recursive: true });
    const file = path.join(dir, 'config.json'); if (fs.existsSync(file)) return console.log('.dray/config.json exists');
    fs.writeFileSync(file, JSON.stringify({
      name: path.basename(process.cwd()),
      images: [{ name: 'app', ecr: 'app', source: { local: true }, dockerfile: 'Dockerfile', context: '.' }],
      workloads: [{ name: 'app', kind: 'deployment', image: 'app', manifests: ['.k8s/deployment.yaml'] }],
      secrets: [], pilets: [],
    }, null, 2));
    console.log(`wrote ${file}`);
  });
};
