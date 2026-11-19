// UI strings come from the server catalogue (web/i18n/*.json), shared with Go templates.
// Components never contain user-facing literals (plan §7.7).
import { api } from './api.ts';

const state = $state<{ locale: string; messages: Record<string, string>; locales: string[] }>({
	locale: 'en',
	messages: {},
	locales: ['en']
});

export const i18n = state;

export async function loadLocale(locale: string): Promise<void> {
	const res = await api.get<{ locale: string; messages: Record<string, string>; locales: string[] }>(
		'/i18n/' + encodeURIComponent(locale)
	);
	state.locale = res.data.locale;
	state.messages = res.data.messages;
	state.locales = res.data.locales;
	document.documentElement.lang = res.data.locale;
}

/** t translates a message ID, replacing {name} placeholders. Missing keys show the key. */
export function t(key: string, args: Record<string, string | number> = {}): string {
	let msg = state.messages[key] ?? key;
	for (const [k, v] of Object.entries(args)) msg = msg.replaceAll('{' + k + '}', String(v));
	return msg;
}

/** errorText maps an API error code (or field code) to a localized message. */
export function errorText(code: string): string {
	const k = 'errors.' + code;
	return state.messages[k] ?? state.messages['errors.internal_error'] ?? code;
}

export function fieldText(code: string): string {
	return state.messages['fields.' + code] ?? state.messages['fields.invalid'] ?? code;
}

export function formatDate(iso: string): string {
	return new Intl.DateTimeFormat(state.locale === 'sw' ? 'sw-KE' : 'en-KE', {
		dateStyle: 'medium',
		timeStyle: 'short',
		timeZone: 'Africa/Nairobi'
	}).format(new Date(iso));
}
