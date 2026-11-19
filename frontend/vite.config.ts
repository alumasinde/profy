import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

// SvelteKit 3: configuration lives here (svelte.config.js is no longer supported).
// The app is a static SPA embedded into the Go binary (plan §6.3); Go serves 200.html for
// /auth, /account, /provider and /admin and the hashed assets under /_app/.
export default defineConfig({
	plugins: [
		sveltekit({
			adapter: adapter({ pages: 'build', assets: 'build', fallback: '200.html', strict: true })
		})
	],
	server: {
		// `npm run dev` proxies API and public pages to the Go server.
		proxy: {
			'/api': 'http://localhost:8080',
			'/theme.css': 'http://localhost:8080',
			'/static': 'http://localhost:8080'
		}
	}
});
