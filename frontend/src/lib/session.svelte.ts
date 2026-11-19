import { api } from './api.ts';

export interface SessionUser {
	public_id: string;
	first_name: string;
	middle_name: string | null;
	last_name: string;
	display_name: string;
	email: string | null;
	phone: string | null;
	email_verified: boolean;
	phone_verified: boolean;
	preferred_locale: string;
	roles: string[];
}

interface SessionState {
	loaded: boolean;
	authenticated: boolean;
	user: SessionUser | null;
	permissions: string[];
	hasPIN: boolean;
	reauthUntil: string | null;
}

const state = $state<SessionState>({
	loaded: false,
	authenticated: false,
	user: null,
	permissions: [],
	hasPIN: false,
	reauthUntil: null
});

export const session = state;

export async function refreshSession(): Promise<void> {
	const res = await api.get<{
		authenticated: boolean;
		user?: SessionUser;
		permissions?: string[];
		has_pin?: boolean;
		reauthenticated_until?: string | null;
	}>('/auth/session');
	state.authenticated = res.data.authenticated;
	state.user = res.data.user ?? null;
	state.permissions = res.data.permissions ?? [];
	state.hasPIN = res.data.has_pin ?? false;
	state.reauthUntil = res.data.reauthenticated_until ?? null;
	state.loaded = true;
}

/** can is for display only. The backend enforces every permission. */
export function can(permission: string): boolean {
	return state.permissions.includes(permission);
}
