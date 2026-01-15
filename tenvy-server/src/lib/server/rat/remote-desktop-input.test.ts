import { describe, expect, it, vi } from 'vitest';

import {
	createQuicTokenMatcher,
	parseQuicTokenConfiguration,
	RemoteDesktopQuicInputService
} from './remote-desktop-input';
import type { RemoteDesktopInputEvent } from '$lib/types/remote-desktop';

describe('parseQuicTokenConfiguration', () => {
	it('returns an empty array for falsy values', () => {
		expect(parseQuicTokenConfiguration(undefined)).toEqual([]);
		expect(parseQuicTokenConfiguration(null)).toEqual([]);
		expect(parseQuicTokenConfiguration('')).toEqual([]);
	});

	it('normalizes comma or whitespace separated strings', () => {
		expect(parseQuicTokenConfiguration(' alpha, beta  ,gamma ')).toEqual([
			'alpha',
			'beta',
			'gamma'
		]);
		expect(parseQuicTokenConfiguration(' one\n two\tthree ')).toEqual(['one', 'two', 'three']);
	});

	it('deduplicates tokens and trims array entries', () => {
		expect(parseQuicTokenConfiguration([' foo ', 'bar', 'foo', ''])).toEqual(['foo', 'bar']);
	});
});

describe('createQuicTokenMatcher', () => {
	it('allows any token when none are configured', () => {
		const matcher = createQuicTokenMatcher([]);
		expect(matcher('')).toBe(true);
		expect(matcher(null)).toBe(true);
		expect(matcher('anything')).toBe(true);
	});

	it('validates tokens using a constant-time comparison', () => {
		const matcher = createQuicTokenMatcher(['secret-token', 'another']);
		expect(matcher('secret-token')).toBe(true);
		expect(matcher('another')).toBe(true);
		expect(matcher('SECRET-TOKEN')).toBe(false);
		expect(matcher('')).toBe(false);
		expect(matcher(null)).toBe(false);
	});

	it('ignores surrounding whitespace in the presented token', () => {
		const matcher = createQuicTokenMatcher(['trim-me']);
		expect(matcher(' trim-me ')).toBe(true);
	});
});

describe('RemoteDesktopQuicInputService.send', () => {
	const createEvents = (count: number): RemoteDesktopInputEvent[] => {
		return Array.from(
			{ length: count },
			(_, index) =>
				({
					type: 'mouse-move',
					capturedAt: index,
					x: index,
					y: index,
					normalized: false
				}) satisfies RemoteDesktopInputEvent
		);
	};

	const registerConnection = (
		service: RemoteDesktopQuicInputService,
		agentId: string,
		sessionId: string,
		stream: Record<string, unknown>
	) => {
		const connection = { agentId, sessionId, session: {}, stream };
		(service as unknown as { connections: Map<string, typeof connection> }).connections.set(
			agentId,
			connection
		);
	};

	it('sends bursts larger than the maximum batch size across multiple chunks', () => {
		const service = new RemoteDesktopQuicInputService();
		const agentId = 'agent-quic-chunk';
		const sessionId = 'session-quic-chunk';
		const write = vi.fn(() => true);
		registerConnection(service, agentId, sessionId, { write });

		const events = createEvents(600);

		const result = service.send(agentId, sessionId, {
			sessionId,
			events,
			sequence: 101
		});

		expect(result.deliveredAll).toBe(true);
		expect(result.deliveredAny).toBe(true);
		expect(result.deliveredEvents).toBe(events.length);
		expect(result.sequence).toBe(101);

		expect(write).toHaveBeenCalledTimes(3);

		const payloads = write.mock.calls.map(([chunk = '']) => {
			const value = typeof chunk === 'string' ? chunk : String(chunk ?? '');
			return JSON.parse(value.trim()) as { events: RemoteDesktopInputEvent[] };
		});
		const delivered = payloads.flatMap((entry) => entry.events);
		expect(delivered).toHaveLength(events.length);
		expect(delivered[0]).toEqual(events[0]);
		expect(delivered[delivered.length - 1]).toEqual(events[events.length - 1]);
	});

	it('stops sending additional chunks when a write fails', () => {
		const service = new RemoteDesktopQuicInputService();
		const agentId = 'agent-quic-fail';
		const sessionId = 'session-quic-fail';
		const write = vi
			.fn<(chunk: string) => boolean>()
			.mockImplementationOnce(() => true)
			.mockImplementationOnce(() => false)
			.mockImplementation(() => true);
		registerConnection(service, agentId, sessionId, { write });

		const events = createEvents(300);

		const result = service.send(agentId, sessionId, {
			sessionId,
			events,
			sequence: 77
		});

		expect(result.deliveredAll).toBe(false);
		expect(result.deliveredAny).toBe(true);
		expect(result.sequence).toBe(77);

		const firstPayload = JSON.parse((write.mock.calls[0][0] as string).trim()) as {
			events: RemoteDesktopInputEvent[];
		};
		expect(result.deliveredEvents).toBe(firstPayload.events.length);

		expect(write).toHaveBeenCalledTimes(3);
		expect(
			(service as unknown as { connections: Map<string, unknown> }).connections.has(agentId)
		).toBe(false);
	});
});
