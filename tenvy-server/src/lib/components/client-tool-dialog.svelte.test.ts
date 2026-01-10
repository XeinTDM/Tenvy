import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';

import ClientToolDialog from './client-tool-dialog.svelte';
import type { Client } from '$lib/data/clients';
import type { AppVncSessionState } from '$lib/types/app-vnc';

type EventListenerMap = Map<string, Set<(event: MessageEvent) => void>>;

class MockEventSource {
	static lastInstance: MockEventSource | null = null;

	readonly url: string;
	readonly withCredentials = false;
	readyState = 0;
	onerror: ((this: EventSource, ev: Event) => void) | null = null;
	onmessage: ((this: EventSource, ev: MessageEvent) => void) | null = null;
	onopen: ((this: EventSource, ev: Event) => void) | null = null;
	private listeners: EventListenerMap = new Map();

	constructor(url: string) {
		this.url = url;
		MockEventSource.lastInstance = this;
	}

	close(): void {
		this.readyState = 2;
	}

	addEventListener(type: string, listener: EventListenerOrEventListenerObject | null): void {
		if (!listener) {
			return;
		}
		const handler = typeof listener === 'function' ? listener : listener.handleEvent.bind(listener);
		let bucket = this.listeners.get(type);
		if (!bucket) {
			bucket = new Set();
			this.listeners.set(type, bucket);
		}
		bucket.add(handler as (event: MessageEvent) => void);
	}

	removeEventListener(type: string, listener: EventListenerOrEventListenerObject | null): void {
		if (!listener) {
			return;
		}
		const handler = typeof listener === 'function' ? listener : listener.handleEvent.bind(listener);
		this.listeners.get(type)?.delete(handler as (event: MessageEvent) => void);
	}

	dispatchEvent(): boolean {
		return true;
	}

	emit(type: string, payload?: unknown): void {
		const listeners = this.listeners.get(type);
		if (!listeners?.size) {
			return;
		}
		const data = payload === undefined ? undefined : JSON.stringify(payload);
		const event = new MessageEvent(type, { data });
		for (const listener of listeners) {
			listener(event);
		}
	}
}

describe('ClientToolDialog - App VNC workspace', () => {
	const client: Client = {
		id: 'client-123',
		codename: 'ORBIT',
		hostname: 'orbit-host',
		ip: '10.0.0.5',
		location: 'Lisbon, PT',
		os: 'Windows 11 Pro',
		platform: 'windows',
		version: '1.0.0',
		status: 'online',
		lastSeen: 'Just now',
		tags: ['vip'],
		risk: 'Medium',
		notes: 'Initial context'
	};

	const activeSession: AppVncSessionState = {
		sessionId: 'session-1',
		agentId: client.id,
		active: true,
		createdAt: '2024-01-01T00:00:00.000Z',
		settings: {
			monitor: 'Primary',
			quality: 'balanced',
			captureCursor: true,
			clipboardSync: false,
			blockLocalInput: false,
			heartbeatInterval: 30
		}
	};

	const originalFetch = globalThis.fetch;
	const originalEventSource = globalThis.EventSource;
	const originalRAF = globalThis.requestAnimationFrame;
	const originalCAF = globalThis.cancelAnimationFrame;

	beforeEach(() => {
		vi.restoreAllMocks();
		globalThis.fetch = vi.fn();
		globalThis.EventSource = MockEventSource as unknown as typeof EventSource;
		globalThis.requestAnimationFrame = ((callback: FrameRequestCallback) => {
			callback(0);
			return 1;
		}) as unknown as typeof requestAnimationFrame;
		globalThis.cancelAnimationFrame = vi.fn();
	});

	afterEach(() => {
		vi.restoreAllMocks();
		globalThis.fetch = originalFetch;
		globalThis.EventSource = originalEventSource;
		if (originalRAF) {
			globalThis.requestAnimationFrame = originalRAF;
		} else {
			// @ts-expect-error allow cleanup when undefined
			delete globalThis.requestAnimationFrame;
		}
		if (originalCAF) {
			globalThis.cancelAnimationFrame = originalCAF;
		} else {
			// @ts-expect-error allow cleanup when undefined
			delete globalThis.cancelAnimationFrame;
		}
	});

	it('manages an App VNC session lifecycle inside the dialog workspace', async () => {
		const fetchMock = globalThis.fetch as Mock;
		fetchMock.mockResolvedValueOnce(
			new Response(JSON.stringify({ session: null }), { status: 200 })
		);
		fetchMock.mockResolvedValueOnce(
			new Response(JSON.stringify({ session: activeSession }), { status: 200 })
		);
		fetchMock.mockResolvedValueOnce(new Response('{}', { status: 200 }));
		fetchMock.mockResolvedValueOnce(
			new Response(JSON.stringify({ session: null }), { status: 200 })
		);

		render(ClientToolDialog, { toolId: 'app-vnc', client });

		await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

		const startButton = await screen.findByRole('button', { name: /start session/i });
		await fireEvent.click(startButton);

		await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));

		const source = MockEventSource.lastInstance;
		expect(source).toBeTruthy();
		source?.emit('frame', {
			frame: {
				image: 'aGVsbG8=',
				encoding: 'png',
				width: 640,
				height: 360
			}
		});
		source?.emit('heartbeat', { timestamp: '2024-01-01T00:00:00.000Z' });

		const frame = await screen.findByRole('img', { name: 'App VNC frame' });
		expect(frame.getAttribute('src')).toContain('data:image/png;base64,aGVsbG8=');

		const viewport = screen.getByTestId('app-vnc-viewport');
		Object.defineProperty(viewport, 'getBoundingClientRect', {
			value: () => ({ left: 0, top: 0, width: 200, height: 200, right: 200, bottom: 200 })
		});

		await fireEvent.pointerDown(viewport, { clientX: 50, clientY: 50, pointerId: 1, button: 0 });
		await waitFor(() => {
			expect(fetchMock).toHaveBeenCalledWith(
				`/api/agents/${client.id}/app-vnc/input`,
				expect.objectContaining({ method: 'POST' })
			);
		});

		const stopButton = await screen.findByRole('button', { name: /stop session/i });
		await fireEvent.click(stopButton);

		await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4));
		source?.emit('end');

		await screen.findByRole('button', { name: /start session/i });
		expect(screen.getByText('Session stopped')).toBeInTheDocument();
	});
});
