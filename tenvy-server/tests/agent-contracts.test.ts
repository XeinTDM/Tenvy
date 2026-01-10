import { describe, expect, it } from 'vitest';

import {
	environmentCommandResponseSchema,
	environmentSnapshotSchema
} from '../src/lib/types/environment.js';
import {
	triggerMonitorCommandResponseSchema,
	triggerMonitorStatusSchema
} from '../src/lib/types/trigger-monitor.js';
import {
	geoCommandResponseSchema,
	geoStatusSchema,
	geoLookupResultSchema
} from '../src/lib/types/ip-geolocation.js';

describe('agent contract smoke tests', () => {
	it('accepts environment list payloads produced by the Go module', () => {
		const payload = {
			action: 'list',
			status: 'ok',
			timestamp: new Date().toISOString(),
			result: {
				variables: [
					{
						key: 'PATH',
						value: '/usr/local/bin',
						scope: 'user',
						length: 15,
						lastModifiedAt: '2024-06-01T12:00:00Z'
					}
				],
				count: 1,
				capturedAt: '2024-06-01T12:00:00Z'
			}
		} satisfies unknown;

		expect(() => environmentCommandResponseSchema.parse(payload)).not.toThrow();
		expect(() => environmentSnapshotSchema.parse(payload.result)).not.toThrow();
	});

	it('accepts trigger monitor status payloads produced by the Go module', () => {
		const payload = {
			action: 'status',
			status: 'ok',
			result: {
				config: {
					feed: 'live',
					refreshSeconds: 5,
					includeScreenshots: false,
					includeCommands: true,
					lastUpdatedAt: '2024-06-01T12:00:00Z',
					watchlist: []
				},
				metrics: [{ id: 'uptime', label: 'Agent Uptime', value: '1h2m' }],
				events: [],
				generatedAt: '2024-06-01T12:00:10Z'
			}
		} satisfies unknown;

		expect(() => triggerMonitorCommandResponseSchema.parse(payload)).not.toThrow();
		expect(() => triggerMonitorStatusSchema.parse(payload.result)).not.toThrow();
	});

	it('accepts geolocation lookup payloads produced by the Go module', () => {
		const payload = {
			action: 'lookup',
			status: 'ok',
			result: {
				ip: '203.0.113.10',
				provider: 'ipinfo',
				city: 'Lisbon',
				region: 'Lisboa',
				country: 'Portugal',
				countryCode: 'PT',
				latitude: 38.7223,
				longitude: -9.1393,
				networkType: 'public',
				isp: 'IberNet',
				asn: 'AS64500',
				timezone: {
					id: 'Europe/Lisbon',
					offset: '+01:00',
					abbreviation: 'WET'
				},
				mapUrl: 'https://www.openstreetmap.org/?mlat=38.7223&mlon=-9.1393#map=9/38.7223/-9.1393',
				retrievedAt: '2024-06-01T12:01:00Z'
			}
		} satisfies unknown;

		expect(() => geoCommandResponseSchema.parse(payload)).not.toThrow();
		expect(() => geoLookupResultSchema.parse(payload.result)).not.toThrow();
	});

	it('accepts geolocation status payloads produced by the Go module', () => {
		const payload = {
			action: 'status',
			status: 'ok',
			result: {
				lastLookup: {
					ip: '198.51.100.4',
					provider: 'maxmind',
					city: 'Berlin',
					region: 'Berlin',
					country: 'Germany',
					countryCode: 'DE',
					latitude: 52.52,
					longitude: 13.405,
					networkType: 'public',
					retrievedAt: '2024-06-01T12:02:00Z'
				},
				providers: ['ipinfo', 'maxmind', 'db-ip'],
				defaultProvider: 'ipinfo',
				generatedAt: '2024-06-01T12:03:00Z'
			}
		} satisfies unknown;

		expect(() => geoCommandResponseSchema.parse(payload)).not.toThrow();
		expect(() => geoStatusSchema.parse(payload.result)).not.toThrow();
	});
});
