// The signed-in provider's listing, shared by the /provider pages.
import { api, ApiError } from '#lib/api.ts';
import type { Listing } from './types.ts';

const state = $state<{ loaded: boolean; listing: Listing | null; error: unknown }>({ loaded: false, listing: null, error: null });

export const provider = state;

export async function loadListing(): Promise<void> {
	try {
		state.listing = (await api.get<Listing>('/provider/profile')).data;
		state.error = null;
	} catch (e) {
		state.listing = null;
		state.error = e instanceof ApiError && e.code === 'no_provider' ? null : e;
	} finally {
		state.loaded = true;
	}
}

export function setListing(l: Listing): void {
	state.listing = l;
}
