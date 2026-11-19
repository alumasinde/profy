// Shapes returned by /api/v1/provider and /api/v1/admin/providers (see docs/api/openapi.yaml).

export interface LocationRef {
	type: 'county' | 'town' | 'area';
	id: string;
	name: string;
	slug: string;
	label: string;
	status: string;
	selectable: boolean;
}

export interface MediaItem {
	id: string;
	purpose: string;
	status: 'active' | 'pending_moderation' | 'rejected' | 'deleted';
	content_type: string;
	bytes: number;
	width?: number;
	height?: number;
	url?: string;
	urls?: Record<string, string>;
	moderation_note?: string | null;
	created_at: string;
}

export interface Offered {
	id: string;
	service_id: string;
	service_name: string;
	service_slug: string;
	category_name: string;
	requires_licence: boolean;
	service_live: boolean;
	custom_title: string | null;
	description: string | null;
	pricing_type: 'fixed' | 'from' | 'range' | 'on_quote';
	price_from: number | null;
	price_to: number | null;
	status: 'active' | 'hidden';
}

export interface Contact {
	channel: 'phone' | 'whatsapp' | 'email';
	value: string;
	visibility: 'public' | 'reveal_on_click' | 'logged_in_only' | 'hidden';
	verified_at: string | null;
}

export interface PortfolioItem {
	id: string;
	caption: string | null;
	position: number;
	media: MediaItem;
}

export interface Badge {
	type: string;
	label: string;
	description: string;
	service_name?: string;
	verified_at: string | null;
	expires_at: string | null;
}

export interface CompletenessItem {
	key: string;
	done: boolean;
	required: boolean;
	weight: number;
}

export interface HistoryEntry {
	from: string | null;
	to: string;
	reason: string | null;
	actor_name: string | null;
	at: string;
}

export interface Listing {
	id: string;
	provider_type: 'individual' | 'business';
	display_name: string;
	slug: string;
	path: string;
	description: string;
	years_experience: number | null;
	status: 'draft' | 'pending_review' | 'published' | 'paused' | 'suspended' | 'archived';
	status_reason: string | null;
	base_area: LocationRef | null;
	service_radius_km: number | null;
	pin: { latitude: number; longitude: number; accuracy_m: number | null; source: string } | null;
	profile_photo: MediaItem | null;
	cover_photo: MediaItem | null;
	services: Offered[];
	coverage: LocationRef[];
	contacts: Contact[];
	portfolio: PortfolioItem[];
	badges: Badge[];
	completeness: { score: number; items: CompletenessItem[]; can_submit: boolean };
	terms_accepted: boolean;
	terms_version: string | null;
	current_terms_version: string;
	meta_title: string | null;
	meta_description: string | null;
	submitted_at: string | null;
	published_at: string | null;
	created_at: string;
	updated_at: string;
	assisted: boolean;
	owner?: { public_id: string; display_name: string; phone: string | null; email: string | null } | null;
	claim?: { destination: string; expires_at: string; claimed_at: string | null } | null;
	history?: HistoryEntry[];
}

/** Editor context: owner pages use '/provider'; staff use '/admin/providers/{id}'. */
export interface EditorTarget {
	base: string;
	staff: boolean;
}

export const ownTarget: EditorTarget = { base: '/provider', staff: false };
export const staffTarget = (id: string): EditorTarget => ({ base: '/admin/providers/' + id, staff: true });

/** profilePath is where the profile resource lives for each target. */
export const profilePath = (t: EditorTarget) => (t.staff ? t.base : t.base + '/profile');

export function formatKES(n: number | null): string {
	return n === null ? '' : new Intl.NumberFormat('en-KE').format(n);
}
