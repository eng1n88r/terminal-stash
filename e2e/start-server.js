// Builds and starts stash for the e2e suite with a clean data dir.
// Building a binary (rather than `go run`) lets Playwright kill the server
// process tree cleanly on Windows.
const fs = require('fs');
const path = require('path');
const { spawn, spawnSync } = require('child_process');

const root = path.join(__dirname, '..');
const dataDir = path.join(__dirname, '.data');
fs.rmSync(dataDir, { recursive: true, force: true });

const exe = path.join(__dirname, '.bin', process.platform === 'win32' ? 'stash-e2e.exe' : 'stash-e2e');
const build = spawnSync('go', ['build', '-o', exe, './src'], {
  cwd: root,
  stdio: 'inherit',
  shell: process.platform === 'win32',
});
if (build.status !== 0) process.exit(build.status ?? 1);

const child = spawn(exe, [], {
  stdio: 'inherit',
  env: { ...process.env, APP_PASSWORD: 'test', DATA_DIR: dataDir, PORT: '7832' },
});
child.on('exit', (code) => process.exit(code ?? 1));
for (const sig of ['SIGTERM', 'SIGINT']) process.on(sig, () => child.kill());
