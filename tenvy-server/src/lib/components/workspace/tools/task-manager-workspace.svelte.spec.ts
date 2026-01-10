import { page } from '@vitest/browser/context';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';

import type { Client } from '$lib/data/clients';
import type { ProcessListResponse } from '$lib/types/task-manager';

import TaskManagerWorkspace from './task-manager-workspace.svelte';

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

function createProcessList(): ProcessListResponse {
	return {
		generatedAt: new Date().toISOString(),
		processes: [
			{
				pid: 1234,
				ppid: 1,
				name: 'nginx',
				cpu: 12.5,
				memory: 42_000_000,
				status: 'running',
				command: '/usr/sbin/nginx',
				user: 'www-data'
			}
		]
	} satisfies ProcessListResponse;
}

describe('task-manager workspace agent requests', () => {
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

	it('loads the process snapshot from the agent API on mount', async () => {
		const payload = createProcessList();
		const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
		fetchMock.mockResolvedValue(
			Promise.resolve({
				ok: true,
				status: 200,
				json: vi.fn().mockResolvedValue(payload)
			} as unknown as Response)
		);

		const { unmount } = render(TaskManagerWorkspace, {
			target: document.body,
			props: { client: baseClient }
		});

		await new Promise((resolve) => setTimeout(resolve, 0));

		expect(globalThis.fetch).toHaveBeenCalledWith(
			`/api/agents/${baseClient.id}/task-manager/processes`
		);

		await expect.element(page.getByText('nginx')).toBeInTheDocument();

		unmount();
	});

	it('surfaces errors returned by the agent task manager endpoint', async () => {
		const payload = createProcessList();
		const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;

		fetchMock.mockResolvedValueOnce(
			Promise.resolve({
				ok: true,
				status: 200,
				json: vi.fn().mockResolvedValue(payload)
			} as unknown as Response)
		);

		fetchMock.mockResolvedValueOnce(
			Promise.resolve({
				ok: false,
				status: 504,
				text: vi.fn().mockResolvedValue('Timed out waiting for agent response')
			} as unknown as Response)
		);

		const { unmount } = render(TaskManagerWorkspace, {
			target: document.body,
			props: { client: baseClient }
		});

		await new Promise((resolve) => setTimeout(resolve, 0));

		const refreshButton = page.getByRole('button', { name: 'Refresh now' });
		refreshButton.click();

		await new Promise((resolve) => setTimeout(resolve, 0));

		await expect
			.element(page.getByText('Timed out waiting for agent response'))
			.toBeInTheDocument();

		unmount();
	});
});
