import { page } from '@vitest/browser/context';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';

import type { Client } from '$lib/data/clients';
import type { ProcessListResponse } from '$lib/types/task-manager';

import TriggerMonitorWorkspace from './trigger-monitor-workspace.svelte';

const originalFetch = globalThis.fetch;

const baseClient: Client = {
	id: 'agent-123',
	codename: 'FOXTROT',
	hostname: 'test-host',
	ip: '192.168.1.10',
	location: 'Test Lab',
	os: 'Linux',
	platform: 'linux',
	version: '1.2.3',
	status: 'online',
	lastSeen: new Date().toISOString(),
	tags: [],
	risk: 'Low'
};

function createStatus() {
	return {
		config: {
			feed: 'live',
			refreshSeconds: 5,
			includeScreenshots: false,
			includeCommands: true,
			watchlist: [],
			lastUpdatedAt: new Date().toISOString()
		},
		metrics: [],
		events: [],
		generatedAt: new Date().toISOString()
	};
}

function createProcessList(): ProcessListResponse {
	return {
		generatedAt: new Date().toISOString(),
		processes: [
			{
				pid: 101,
				ppid: 1,
				name: 'systemd',
				command: '/usr/lib/systemd/systemd',
				cpu: 0.8,
				memory: 128_000,
				status: 'running',
				user: 'root'
			}
		]
	} satisfies ProcessListResponse;
}

describe('trigger-monitor workspace suggestions', () => {
	beforeEach(() => {
		globalThis.fetch = vi.fn();
	});

	afterEach(() => {
		if (originalFetch) {
			globalThis.fetch = originalFetch;
		} else {
			// @ts-expect-error cleanup in tests
			delete globalThis.fetch;
		}
	});

	it('loads catalogue and process suggestions when available', async () => {
		const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
		fetchMock.mockImplementation((input) => {
			const url = typeof input === 'string' ? input : (input?.url ?? '');
			if (url.endsWith('/misc/trigger-monitor')) {
				return Promise.resolve(
					new Response(JSON.stringify(createStatus()), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				);
			}
			if (url.endsWith('/downloads')) {
				return Promise.resolve(
					new Response(
						JSON.stringify({
							downloads: [
								{
									id: 'atlas.exe',
									displayName: 'Atlas Explorer',
									version: '2.3.1',
									description: 'Reconnaissance utility'
								}
							]
						}),
						{
							status: 200,
							headers: { 'Content-Type': 'application/json' }
						}
					)
				);
			}
			if (url.endsWith('/task-manager/processes')) {
				return Promise.resolve(
					new Response(JSON.stringify(createProcessList()), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				);
			}
			throw new Error(`Unhandled fetch: ${url}`);
		});

		const { unmount } = render(TriggerMonitorWorkspace, {
			target: document.body,
			props: { client: baseClient }
		});

		await new Promise((resolve) => setTimeout(resolve, 0));

		const manageButton = page.getByRole('button', { name: 'Manage watchlist' });
		manageButton.click();

		await new Promise((resolve) => setTimeout(resolve, 0));

		await expect.element(page.getByText('Atlas Explorer')).toBeInTheDocument();
		await expect.element(page.getByText('systemd')).toBeInTheDocument();
		unmount();
	});

	it('surfaces catalogue errors once and avoids repeated auto-fetching', async () => {
		const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
		fetchMock.mockImplementation((input) => {
			const url = typeof input === 'string' ? input : (input?.url ?? '');
			if (url.endsWith('/misc/trigger-monitor')) {
				return Promise.resolve(
					new Response(JSON.stringify(createStatus()), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				);
			}
			if (url.endsWith('/downloads')) {
				return Promise.resolve(
					new Response(JSON.stringify({ message: 'Service unavailable' }), {
						status: 503,
						headers: { 'Content-Type': 'application/json' }
					})
				);
			}
			if (url.endsWith('/task-manager/processes')) {
				return Promise.resolve(
					new Response(JSON.stringify(createProcessList()), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				);
			}
			throw new Error(`Unhandled fetch: ${url}`);
		});

		const { unmount } = render(TriggerMonitorWorkspace, {
			target: document.body,
			props: { client: baseClient }
		});

		await new Promise((resolve) => setTimeout(resolve, 0));

		const manageButton = page.getByRole('button', { name: 'Manage watchlist' });
		manageButton.click();

		await new Promise((resolve) => setTimeout(resolve, 0));

		await expect
			.element(page.getByText('Downloads catalogue unavailable', { exact: false }))
			.toBeInTheDocument();
		await expect.element(page.getByText('systemd')).toBeInTheDocument();

		const cancelButton = page.getByRole('button', { name: 'Cancel' });
		cancelButton.click();

		await new Promise((resolve) => setTimeout(resolve, 0));

		manageButton.click();

		await new Promise((resolve) => setTimeout(resolve, 0));

		const downloadCalls = fetchMock.mock.calls.filter(([input]) => {
			const url = typeof input === 'string' ? input : (input?.url ?? '');
			return typeof url === 'string' && url.includes('/downloads');
		});
		expect(downloadCalls).toHaveLength(1);

		unmount();
	});
});
