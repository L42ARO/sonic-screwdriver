// @ts-check
// The sonic-screwdriver docs: https://sonicscrew.click/docs
// `npm run build` writes a static site to dist/ (pages, search index, llms.txt files and a
// Markdown copy of each page); scripts/publish.mjs copies it into the landing site.
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLlmsTxt from 'starlight-llms-txt';

export default defineConfig({
	site: 'https://sonicscrew.click',
	base: '/docs',
	trailingSlash: 'always',
	integrations: [
		starlight({
			title: 'sonic-screwdriver',
			description: 'An API for software that has no API: typed operations carried out on desktop programs by a vision agent.',
			logo: { src: './src/assets/logo.svg' },
			favicon: '/favicon.svg',
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/L42ARO/sonic-screwdriver' }],
			editLink: { baseUrl: 'https://github.com/L42ARO/sonic-screwdriver/edit/main/docs/' },
			customCss: ['./src/styles/theme.css'],
			components: { PageTitle: './src/components/PageTitle.astro' },
			sidebar: [
				{ label: 'Getting started', link: '/' },
				{ label: 'Writing operations', slug: 'operations' },
				{ label: 'The agent', slug: 'agent' },
				{ label: 'Self-hosting', slug: 'self-hosting' },
				{ label: 'Security and FAQ', slug: 'security' },
			],
			plugins: [
				starlightLlmsTxt({
					projectName: 'sonic-screwdriver',
					description:
						'sonic-screwdriver turns desktop programs that have no API into typed functions. A backend calls an operation through the Python, TypeScript or Go SDK; the compute-box server runs a vision model that drives the program on a Windows PC through the SonicScrew Local Agent, using screenshots, mouse and keyboard.',
					details: [
						'Each page is also available as Markdown:',
						'',
						'- [Getting started](https://sonicscrew.click/docs/index.md): what it is, concepts, quickstart with Python, TypeScript and Go',
						'- [Writing operations](https://sonicscrew.click/docs/operations.md): declaring operations, programs and machines, sync, errors, writing instructions',
						'- [The agent](https://sonicscrew.click/docs/agent.md): installing and connecting the Windows agent, updates, connection problems',
						'- [Self-hosting](https://sonicscrew.click/docs/self-hosting.md): Docker, settings, spend limits, HTTPS',
						'- [Security and FAQ](https://sonicscrew.click/docs/security.md): what the agent can access, data, credentials, signed updates',
					].join('\n'),
					optionalLinks: [
						{ label: 'Source code', url: 'https://github.com/L42ARO/sonic-screwdriver', description: 'compute-box (Go), local-agent (Rust), SDKs and examples' },
					],
				}),
			],
		}),
	],
});
