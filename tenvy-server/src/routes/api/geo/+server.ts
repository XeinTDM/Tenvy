import { error, json } from '@sveltejs/kit';
import {
	normalizeIp,
	validateLookupTarget,
	getCachedGeo,
	fetchGeoData,
	setCachedGeo,
	type GeoLookupPayload
} from './lookup';
import type { RequestHandler } from './$types';

function isString(value: unknown): value is string {
	return typeof value === 'string' || value instanceof String;
}

export const POST: RequestHandler = async ({ request, fetch }) => {
	let payload: unknown;
	try {
		payload = await request.json();
	} catch {
		throw error(400, 'Invalid request body');
	}

	if (!Array.isArray(payload)) {
		throw error(400, 'Expected an array of IP addresses');
	}

	const uniqueIps = new Set<string>();
	for (const candidate of payload) {
		if (!isString(candidate)) {
			continue;
		}
		const normalized = normalizeIp(candidate);
		if (!normalized) {
			continue;
		}
		try {
			validateLookupTarget(normalized);
		} catch {
			continue;
		}
		uniqueIps.add(normalized);
	}

	if (uniqueIps.size === 0) {
		return json({});
	}

	const results: Record<string, GeoLookupPayload> = {};
	const pending: Promise<void>[] = [];

	for (const ip of uniqueIps) {
		const cached = getCachedGeo(ip);
		if (cached) {
			results[ip] = cached;
			continue;
		}

		pending.push(
			(async () => {
				const lookup = await fetchGeoData(fetch, ip);
				setCachedGeo(ip, lookup);
				results[ip] = lookup;
			})()
		);
	}

	if (pending.length > 0) {
		await Promise.allSettled(pending);
	}

	return json(results satisfies Record<string, GeoLookupPayload>);
};
