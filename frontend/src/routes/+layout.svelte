<script lang="ts">
	import '../app.css';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api } from '#lib/api.ts';
	import { i18n, loadLocale, t } from '#lib/i18n.svelte.ts';
	import { can, refreshSession, session } from '#lib/session.svelte.ts';

	let { children } = $props();
	let ready = $state(false);
	let siteName = $state('');

	const protectedPrefixes = ['/account', '/provider', '/admin'];

	$effect(() => {
		(async () => {
			try {
				const [s] = await Promise.all([
					api.get<{ settings: Record<string, unknown> }>('/settings/public'),
					refreshSession()
				]);
				siteName = String(s.data.settings.site_name ?? '');
				const locale = session.user?.preferred_locale ?? String(s.data.settings.default_locale ?? 'en');
				await loadLocale(locale).catch(() => loadLocale('en'));
				document.title = siteName;
			} finally {
				ready = true;
			}
		})();
	});

	// Redirect anonymous visitors away from protected areas (display only; the API enforces).
	$effect(() => {
		if (!ready || session.authenticated) return;
		const path = page.url.pathname;
		if (protectedPrefixes.some((p) => path === p || path.startsWith(p + '/'))) {
			goto('/auth/login?next=' + encodeURIComponent(path + page.url.search), { replaceState: true });
		}
	});

	async function logout() {
		await api.post('/auth/logout');
		await refreshSession();
		goto('/auth/login');
	}

	const adminPerms = ['users.view_any', 'roles.view', 'audit_logs.view', 'platform.settings.view', 'platform.feature_flags.view',
		'pages.view', 'navigation.view', 'categories.view', 'services.view', 'locations.view', 'seo.manage_redirects'];
	const isAdmin = $derived(adminPerms.some((p) => can(p)));
	const current = (href: string) => (page.url.pathname === href || page.url.pathname.startsWith(href + '/') ? 'page' : undefined);
</script>

<svelte:head><title>{siteName}</title></svelte:head>

{#if !ready || Object.keys(i18n.messages).length === 0}
	<p class="container muted" aria-live="polite">…</p>
{:else}
	<header class="topbar">
		<div class="container topbar-inner">
			<a class="brand" href="/" data-sveltekit-reload>{siteName}</a>
			<nav class="topnav" aria-label={t('app.nav.main_label')}>
				{#if session.authenticated}
					<a href="/account" aria-current={page.url.pathname === '/account' ? 'page' : undefined}>{t('app.nav.account')}</a>
					<a href="/account/security" aria-current={current('/account/security')}>{t('app.nav.security')}</a>
					{#if isAdmin}<a href="/admin" aria-current={current('/admin')}>{t('app.nav.admin')}</a>{/if}
					<button type="button" onclick={logout}>{t('app.nav.logout')}</button>
				{:else}
					<a href="/auth/login" aria-current={current('/auth/login')}>{t('app.nav.login')}</a>
					<a href="/auth/register" aria-current={current('/auth/register')}>{t('app.nav.register')}</a>
				{/if}
			</nav>
		</div>
	</header>
	<main class="container">
		{@render children()}
	</main>
{/if}
