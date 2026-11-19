// Copies the SvelteKit build into ../web/app for go:embed, keeping web/app/.gitkeep.
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(root, 'build');
const dest = join(root, '..', 'web', 'app');
if (!existsSync(join(src, '200.html'))) {
	console.error('build/200.html missing: run vite build first');
	process.exit(1);
}
mkdirSync(dest, { recursive: true });
for (const entry of readdirSync(dest)) {
	if (entry !== '.gitkeep') rmSync(join(dest, entry), { recursive: true, force: true });
}
cpSync(src, dest, { recursive: true });
writeFileSync(join(dest, '.gitkeep'), '');
console.log('published SvelteKit build to web/app');
