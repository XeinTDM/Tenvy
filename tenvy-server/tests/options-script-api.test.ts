import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { join } from 'node:path';
import { rm } from 'node:fs/promises';
import type { RequestEvent } from '@sveltejs/kit';

class MockRegistryError extends Error {
	status = 400;
}

const authorizeAgent = vi.fn();

vi.mock('../src/lib/server/rat/store.js', () => ({
	registry: {
		authorizeAgent
	},
	RegistryError: MockRegistryError
}));

const modulePromise = import('../src/routes/api/agents/[id]/options/script/+server.js');

function createEvent(init: Partial<RequestEvent>) {
	return {
		params: {},
		request: new Request('https://controller.test', { method: 'GET' }),
		setHeaders: vi.fn(),
		...init
	} as unknown as any; // eslint-disable-line @typescript-eslint/no-explicit-any
}

describe('options script staging API', () => {
	beforeEach(() => {
		authorizeAgent.mockReset();
	});

	afterEach(async () => {
		await rm(join(process.cwd(), '.data', 'options-scripts'), { recursive: true, force: true });
	});

	it('stages scripts and allows authorized retrieval', async () => {
		const { POST, GET } = await modulePromise;

		const file = new File([new TextEncoder().encode('Write-Host "hello"')], 'utility.ps1', {
			type: 'text/x-powershell'
		});
		const form = new FormData();
		form.set('script', file);

		const postEvent = createEvent({
			params: { id: 'agent-script' },
			request: new Request('https://controller.test', {
				method: 'POST',
				body: form
			})
		});
		const postResponse = await POST(postEvent);

		expect(postResponse.status).toBe(201);
		const staged = (await postResponse.json()) as {
			stagingToken: string;
			fileName: string;
			size: number;
			type?: string;
			sha256?: string;
		};
		expect(staged.stagingToken).toMatch(/[a-f0-9-]{8,}/i);
		expect(staged.fileName).toBe('utility.ps1');
		expect(staged.size).toBeGreaterThan(0);

		authorizeAgent.mockImplementation(() => undefined);

		const getEvent = createEvent({
			params: { id: 'agent-script' },
			request: new Request(
				`https://controller.test/api?token=${encodeURIComponent(staged.stagingToken)}`,
				{
					method: 'GET',
					headers: {
						Authorization: 'Bearer agent-key'
					}
				}
			)
		});
		const getResponse = await GET(getEvent);

		expect(authorizeAgent).toHaveBeenCalledWith('agent-script', 'agent-key');

		const buffer = new Uint8Array(await getResponse.arrayBuffer());
		expect(buffer.byteLength).toBeGreaterThan(0);
		const headers = (getEvent.setHeaders as Mock).mock.calls[0]?.[0] as Record<string, string>;
		expect(headers['X-Tenvy-Script-Name']).toBe('utility.ps1');
		expect(headers['X-Tenvy-Script-Type']).toBe('text/x-powershell');
		expect(headers['X-Tenvy-Script-Size']).toBe(String(buffer.byteLength));
	});

	it('rejects files that exceed the maximum size', async () => {
		const { POST } = await modulePromise;

		const large = new Uint8Array(300_000);
		const file = new File([large], 'oversize.ps1', { type: 'text/plain' });
		const form = new FormData();
		form.set('script', file);

		const event = createEvent({
			params: { id: 'agent-large' },
			request: new Request('https://controller.test', {
				method: 'POST',
				body: form
			})
		});

		await expect(POST(event)).rejects.toMatchObject({ status: 413 });
	});
});
