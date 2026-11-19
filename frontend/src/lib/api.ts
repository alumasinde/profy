// Thin client for the Go API (/api/v1). The backend enforces every rule; this client only
// attaches the CSRF token and normalizes the error envelope (plan §15.1).

export interface ApiErrorBody {
	code: string;
	message: string;
	fields?: Record<string, string>;
	request_id?: string;
}

export class ApiError extends Error {
	status: number;
	code: string;
	fields: Record<string, string>;
	requestId?: string;
	retryAfter?: number;

	constructor(status: number, body: ApiErrorBody, retryAfter?: number) {
		super(body.message);
		this.status = status;
		this.code = body.code;
		this.fields = body.fields ?? {};
		this.requestId = body.request_id;
		this.retryAfter = retryAfter;
	}
}

export interface Envelope<T> {
	data: T;
	meta?: Record<string, unknown>;
}

function csrfToken(): string {
	const m = document.cookie.match(/(?:^|;\s*)(?:__Host-)?hl_csrf=([^;]+)/);
	return m ? decodeURIComponent(m[1]) : '';
}

export async function request<T>(method: string, path: string, body?: unknown): Promise<Envelope<T>> {
	const headers: Record<string, string> = { Accept: 'application/json' };
	const form = body instanceof FormData;
	if (body !== undefined && !form) headers['Content-Type'] = 'application/json';
	if (method !== 'GET') headers['X-CSRF-Token'] = csrfToken();
	let res: Response;
	try {
		res = await fetch('/api/v1' + path, {
			method,
			headers,
			credentials: 'same-origin',
			body: body === undefined ? undefined : form ? (body as FormData) : JSON.stringify(body)
		});
	} catch {
		throw new ApiError(0, { code: 'network', message: 'Network error' });
	}
	if (res.status === 204) return { data: undefined as T };
	const json = await res.json().catch(() => null);
	if (!res.ok) {
		const retry = Number(res.headers.get('Retry-After') ?? '') || undefined;
		throw new ApiError(res.status, json?.error ?? { code: 'internal_error', message: res.statusText }, retry);
	}
	return json as Envelope<T>;
}

export const api = {
	get: <T>(p: string) => request<T>('GET', p),
	post: <T>(p: string, b?: unknown) => request<T>('POST', p, b ?? {}),
	put: <T>(p: string, b?: unknown) => request<T>('PUT', p, b ?? {}),
	patch: <T>(p: string, b?: unknown) => request<T>('PATCH', p, b ?? {}),
	del: <T>(p: string) => request<T>('DELETE', p),
	/** upload sends multipart form data (the browser sets the boundary). */
	upload: <T>(p: string, f: FormData) => request<T>('POST', p, f)
};

/** safeNext accepts only same-origin relative paths for post-login redirects. */
export function safeNext(raw: string | null, fallback = '/account'): string {
	if (!raw || !raw.startsWith('/') || raw.startsWith('//') || raw.startsWith('/\\')) return fallback;
	return raw;
}
