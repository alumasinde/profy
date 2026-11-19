<script lang="ts">
	import { t } from '#lib/i18n.svelte.ts';
	import { can } from '#lib/session.svelte.ts';

	// Shown only when the user holds the permission; the API enforces every action.
	const sections = [
		{ href: '/admin/providers', label: 'app.admin.providers', perm: 'providers.view_any' },
		{ href: '/admin/verifications', label: 'app.admin.verifications', perm: 'providers.view_any' },
		{ href: '/admin/media', label: 'app.admin.media', perm: 'media.moderate' },
		{ href: '/admin/settings', label: 'app.admin.settings', perm: 'platform.settings.view' },
		{ href: '/admin/feature-flags', label: 'app.admin.flags', perm: 'platform.feature_flags.view' },
		{ href: '/admin/pages', label: 'app.admin.pages', perm: 'pages.view' },
		{ href: '/admin/navigation', label: 'app.admin.navigation', perm: 'navigation.view' },
		{ href: '/admin/categories', label: 'app.admin.categories', perm: 'categories.view' },
		{ href: '/admin/services', label: 'app.admin.services', perm: 'services.view' },
		{ href: '/admin/locations', label: 'app.admin.locations', perm: 'locations.view' },
		{ href: '/admin/redirects', label: 'app.admin.redirects', perm: 'seo.manage_redirects' },
		{ href: '/admin/users', label: 'app.admin.users', perm: 'users.view_any' },
		{ href: '/admin/roles', label: 'app.admin.roles', perm: 'roles.view' },
		{ href: '/admin/audit-logs', label: 'app.admin.audit', perm: 'audit_logs.view' }
	];
	const visible = $derived(sections.filter((s) => can(s.perm)));
</script>

<h1>{t('app.admin.title')}</h1>
<ul class="admin-links">
	{#each visible as s (s.href)}<li><a class="card" href={s.href}>{t(s.label)}</a></li>{/each}
</ul>
{#if visible.length === 0}<p class="muted">{t('errors.forbidden')}</p>{/if}

<style>
	.admin-links {
		list-style: none;
		padding: 0;
		display: grid;
		gap: 0.75rem;
		grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
	}
	.admin-links a {
		display: block;
		font-weight: 600;
		text-decoration: none;
	}
</style>
