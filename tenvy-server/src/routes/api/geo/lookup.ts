import { error } from '@sveltejs/kit';
import { isIP } from 'node:net';
import { isLikelyPrivateIp } from '$lib/utils/ip';

export const CACHE_TTL_SECONDS = 15 * 60;
const CACHE_TTL_MS = CACHE_TTL_SECONDS * 1000;

export type GeoLookupPayload = {
	countryName: string | null;
	countryCode: string | null;
	isProxy: boolean;
};

type CacheEntry = {
	expiresAt: number;
	payload: GeoLookupPayload;
};

const geoCache = new Map<string, CacheEntry>();

export function normalizeIp(rawValue: string | null | undefined): string {
	const raw = (rawValue ?? '').trim();
	if (!raw) {
		return '';
	}

	const withoutBrackets = raw.startsWith('[') && raw.endsWith(']') ? raw.slice(1, -1) : raw;
	return withoutBrackets.toLowerCase();
}

export function validateLookupTarget(ip: string): void {
	if (!ip || isIP(ip) === 0 || isLikelyPrivateIp(ip)) {
		throw error(400, 'Invalid IP address');
	}
}

export function getCachedGeo(ip: string): GeoLookupPayload | null {
	const cached = geoCache.get(ip);
	if (!cached) {
		return null;
	}

	if (cached.expiresAt < Date.now()) {
		geoCache.delete(ip);
		return null;
	}

	return cached.payload;
}

export function setCachedGeo(ip: string, payload: GeoLookupPayload): void {
	geoCache.set(ip, {
		expiresAt: Date.now() + CACHE_TTL_MS,
		payload
	});
}

export async function fetchGeoData(fetchFn: typeof fetch, ip: string): Promise<GeoLookupPayload> {
	const endpoint = new URL(`https://ip-api.com/json/${encodeURIComponent(ip)}`);
	endpoint.searchParams.set('fields', 'status,message,country,countryCode,proxy');

	let response: Response;
	try {
		response = await fetchFn(endpoint.toString(), {
			headers: { Accept: 'application/json' }
		});
	} catch {
		throw error(502, 'Failed to contact geo provider');
	}

	if (!response.ok) {
		throw error(502, 'Geo provider returned an unexpected response');
	}

	let payload: {
		status?: 'success' | 'fail';
		message?: string;
		country?: string;
		countryCode?: string;
		proxy?: boolean;
	};

	try {
		payload = (await response.json()) as typeof payload;
	} catch {
		throw error(502, 'Geo provider returned malformed data');
	}

	if (payload.status !== 'success') {
		throw error(502, payload.message ?? 'Geo lookup failed');
	}

	const countryName = payload.country?.trim() || null;
	const countryCode = payload.countryCode?.trim().toUpperCase() || null;

	return {
		countryName,
		countryCode,
		isProxy: payload.proxy === true
	} satisfies GeoLookupPayload;
}

export const __testing = {
	clearCache: () => geoCache.clear()
};
