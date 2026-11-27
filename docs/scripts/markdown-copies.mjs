// Writes a plain-Markdown copy of each docs page next to the built site, for AI tools and
// the "Copy page" button: src/content/docs/agent.mdx -> dist/agent.md (served as
// /docs/agent.md). MDX components are turned into plain Markdown and site links made
// absolute. Runs after `astro build` (see package.json).
import { readdir, readFile, writeFile, mkdir } from 'node:fs/promises';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const pages = join(root, 'src/content/docs');
const out = join(root, 'dist');
const site = 'https://sonicscrew.click';

async function* files(dir) {
	for (const entry of await readdir(dir, { withFileTypes: true })) {
		const path = join(dir, entry.name);
		if (entry.isDirectory()) yield* files(path);
		else if (/\.mdx?$/.test(entry.name)) yield path;
	}
}

function toMarkdown(source) {
	const match = source.match(/^---\r?\n([\s\S]*?)\r?\n---\r?\n/);
	const front = match ? match[1] : '';
	let body = match ? source.slice(match[0].length) : source;
	const field = (name) => front.match(new RegExp(`^${name}:\\s*(.*)$`, 'm'))?.[1].trim() ?? '';
	body = body
		.replace(/^import .*$/gm, '')
		.replace(/^\s*<\/?(Tabs|Steps)\b[^>]*>\s*$/gm, '')
		.replace(/^(\s*)<TabItem label="([^"]+)">\s*$/gm, '$1**$2:**\n')
		.replace(/^\s*<\/TabItem>\s*$/gm, '')
		.replace(/<Aside[^>]*title="([^"]+)"[^>]*>/g, '**$1:** ')
		.replace(/<Aside[^>]*type="caution"[^>]*>/g, '**Caution:** ')
		.replace(/<Aside[^>]*>/g, '**Note:** ')
		.replace(/<\/Aside>/g, '')
		.replace(/\]\(\/docs\//g, `](${site}/docs/`)
		.replace(/\n{3,}/g, '\n\n')
		.trim();
	return `# ${field('title')}\n\n> ${field('description')}\n\n${body}\n`;
}

let count = 0;
for await (const path of files(pages)) {
	const id = relative(pages, path).replace(/\\/g, '/').replace(/\.mdx?$/, '');
	const target = join(out, `${id}.md`);
	await mkdir(dirname(target), { recursive: true });
	await writeFile(target, toMarkdown(await readFile(path, 'utf8')));
	count++;
}
console.log(`markdown copies: ${count} pages`);
