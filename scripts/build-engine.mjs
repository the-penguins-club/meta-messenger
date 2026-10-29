import { mkdirSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = fileURLToPath(new URL('../', import.meta.url));
mkdirSync(path.join(root, 'bin'), { recursive: true });
const target = path.join(root, 'bin', `messenger-engine${process.platform === 'win32' ? '.exe' : ''}`);
const result = spawnSync('go', ['build', '-trimpath', '-o', target, './cmd/messenger-engine'], {
  cwd: path.join(root, 'engine'), stdio: 'inherit',
});
if (result.error) console.error(`Cannot build Go engine: ${result.error.message}`);
process.exit(result.status ?? 1);
