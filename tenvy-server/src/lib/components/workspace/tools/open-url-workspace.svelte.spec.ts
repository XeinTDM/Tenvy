import { page } from '@vitest/browser/context';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';

import type { Client } from '$lib/data/clients';
import type { WorkspaceLogEntry } from '$lib/workspace/types';

import ClientToolWorkspace from '$lib/components/workspace/client-tool-workspace.svelte';
import { getClientTool } from '$lib/data/client-tools';
import OpenUrlWorkspace from './open-url-workspace.svelte';

const originalFetch = globalThis.fetch;

vi.mock('$lib/utils/agent-commands.js', () => ({
	notifyToolActivationCommand: vi.fn()
}));

const baseClient: Client = {
	id: 'agent-321',
	codename: 'SIERRA',
	hostname: 'demo-host',
	ip: '10.1.2.3',
	location: 'QA Lab',
	os: 'Windows',
	platform: 'windows',
	version: '1.0.0',
	status: 'online',
	lastSeen: new Date().toISOString(),
	tags: [],
	risk: 'Low'
};

describe('open-url workspace', () => {
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

	it('dispatches a URL launch immediately when the agent session is active', async () => {
		const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
		fetchMock.mockResolvedValue(
			Promise.resolve({
				ok: true,
				json: vi.fn().mockResolvedValue({
					delivery: 'session',
					command: {
						id: 'cmd-open-url',
						name: 'open-url',
						payload: { url: 'https://example.com' },
						createdAt: new Date().toISOString()
					}
				})
			} as unknown as Response)
		);

		const { component, unmount } = render(OpenUrlWorkspace, {
			target: document.body,
			props: { client: baseClient }
		});
		const logs: WorkspaceLogEntry[][] = [];
		component.$on('logchange', (event) => {
			logs.push(event.detail);
		});

		const urlField = page.getByLabelText('URL');
		await expect.element(urlField).toBeInTheDocument();
		await urlField.fill('https://example.com');

		const queueButton = page.getByRole('button', { name: 'Queue launch' });
		await expect.element(queueButton).toBeInTheDocument();
		await queueButton.click();

		await new Promise((resolve) => setTimeout(resolve, 0));

		expect(fetchMock).toHaveBeenCalledWith(`/api/agents/${baseClient.id}/commands`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({
				name: 'open-url',
				payload: { url: 'https://example.com' }
			})
		});

		const finalLog = logs.at(-1);
		expect(finalLog?.[0]).toMatchObject({
			status: 'in-progress',
			detail: 'Launch dispatched to live session'
		});

		unmount();
	});

	it('records a queued delivery when the command awaits the next agent poll', async () => {
		const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
		fetchMock.mockResolvedValue(
			Promise.resolve({
				ok: true,
				json: vi.fn().mockResolvedValue({
					delivery: 'queued',
					command: {
						id: 'cmd-open-url-queued',
						name: 'open-url',
						payload: { url: 'https://queued.example.com' },
						createdAt: new Date().toISOString()
					}
				})
			} as unknown as Response)
		);

		const { component, unmount } = render(OpenUrlWorkspace, {
			target: document.body,
			props: { client: baseClient }
		});
		const logs: WorkspaceLogEntry[][] = [];
		component.$on('logchange', (event) => {
			logs.push(event.detail);
		});

		const urlField = page.getByLabelText('URL');
		await urlField.fill('https://queued.example.com');

		const queueButton = page.getByRole('button', { name: 'Queue launch' });
		await queueButton.click();

		await new Promise((resolve) => setTimeout(resolve, 0));

		const finalLog = logs.at(-1);
		expect(finalLog?.[0]).toMatchObject({
			status: 'in-progress',
			detail: 'Awaiting agent execution'
		});

		const [, options] = fetchMock.mock.calls[0] ?? [];
		expect(options?.body).toBe(
			JSON.stringify({
				name: 'open-url',
				payload: { url: 'https://queued.example.com' }
			})
		);

		unmount();
	});

	it('renders the open-url workspace when loaded through ClientToolWorkspace', async () => {
		const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
		fetchMock.mockResolvedValue(
			Promise.resolve({
				ok: true,
				json: vi.fn().mockResolvedValue({
					delivery: 'session',
					command: {
						id: 'cmd-open-url',
						name: 'open-url',
						payload: { url: 'https://example.com' },
						createdAt: new Date().toISOString()
					}
				})
			} as unknown as Response)
		);

		const tool = getClientTool('open-url');
		const { unmount } = render(ClientToolWorkspace, {
			target: document.body,
			props: {
				client: baseClient,
				tool
			}
		});

		const urlField = page.getByLabelText('URL');
		await expect.element(urlField).toBeInTheDocument();

		const queueButton = page.getByRole('button', { name: 'Queue launch' });
		await expect.element(queueButton).toBeInTheDocument();

		const fallbackAlert = page.getByText('Workspace not implemented');
		await expect.element(fallbackAlert).not.toBeInTheDocument();

		unmount();
	});
});
