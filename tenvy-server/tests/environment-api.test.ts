import { beforeEach, describe, expect, it, vi } from 'vitest';

const requireViewer = vi.fn((user: { id: string } | null | undefined) => user ?? { id: 'viewer' });
const requireOperator = vi.fn(
	(user: { id: string } | null | undefined) => user ?? { id: 'operator' }
);

vi.mock('../src/lib/server/authorization.js', () => ({
	requireViewer,
	requireOperator
}));

const dispatchEnvironmentCommand = vi.fn();

class MockEnvironmentAgentError extends Error {
	status: number;

	constructor(message: string, status = 500) {
		super(message);
		this.status = status;
	}
}

vi.mock('../src/lib/server/rat/environment.js', () => ({
	dispatchEnvironmentCommand,
	EnvironmentAgentError: MockEnvironmentAgentError
}));

const modulePromise = import('../src/routes/api/agents/[id]/misc/environment-variables/+server');

type Handler =
	Awaited<typeof modulePromise> extends infer T
		? T extends { GET?: infer G; POST?: infer P }
			? G | P
			: never
		: never;

function createEvent<T extends Handler>(
	handler: T,
	init: Partial<Parameters<T>[0]> & { method?: string } = {}
) {
	const method = init.method ?? 'GET';
	const request =
		init.request instanceof Request
			? init.request
			: new Request('https://controller.test/api', { method });
	return {
		params: { id: 'agent-1', ...(init.params ?? {}) },
		request,
		locals: init.locals ?? { user: { id: 'tester' } },
		...init
	} as Parameters<T>[0];
}

describe('environment variables API', () => {
	beforeEach(() => {
		requireViewer.mockClear();
		requireOperator.mockClear();
		dispatchEnvironmentCommand.mockReset();
	});

	it('retrieves environment snapshot', async () => {
		const { GET } = await modulePromise;
		if (!GET) throw new Error('GET handler missing');

		const snapshot = {
			variables: [],
			count: 0,
			capturedAt: '2024-06-01T12:00:00Z'
		} satisfies Awaited<ReturnType<typeof dispatchEnvironmentCommand>>;
		dispatchEnvironmentCommand.mockResolvedValueOnce(snapshot);

		const response = await GET(createEvent(GET));

		expect(requireViewer).toHaveBeenCalledWith({ id: 'tester' });
		expect(dispatchEnvironmentCommand).toHaveBeenCalledWith(
			'agent-1',
			expect.objectContaining({ action: 'list', timestamp: expect.any(String) })
		);
		expect(await response.json()).toEqual(snapshot);
	});

	it('queues environment set mutations', async () => {
		const { POST } = await modulePromise;
		if (!POST) throw new Error('POST handler missing');

		const mutation = {
			key: 'PATH',
			scope: 'machine',
			value: 'C:/bin',
			operation: 'set',
			mutatedAt: '2024-06-01T12:00:00Z'
		} satisfies Awaited<ReturnType<typeof dispatchEnvironmentCommand>>;

		dispatchEnvironmentCommand.mockResolvedValueOnce(mutation);

		const body = {
			action: 'set',
			key: 'PATH',
			value: 'C:/bin',
			scope: 'machine',
			timestamp: new Date().toISOString()
		};

		const response = await POST(
			createEvent(POST, {
				method: 'POST',
				request: new Request('https://controller.test/api', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify(body)
				})
			})
		);

		expect(requireOperator).toHaveBeenCalledWith({ id: 'tester' });
		expect(dispatchEnvironmentCommand).toHaveBeenCalledWith(
			'agent-1',
			expect.objectContaining({
				action: 'set',
				key: 'PATH',
				value: 'C:/bin',
				scope: 'machine',
				timestamp: expect.any(String)
			}),
			{
				operatorId: 'tester'
			}
		);
		expect(await response.json()).toEqual(mutation);
	});

	it('propagates agent errors', async () => {
		const { GET } = await modulePromise;
		if (!GET) throw new Error('GET handler missing');

		dispatchEnvironmentCommand.mockRejectedValueOnce(new MockEnvironmentAgentError('offline', 503));

		await expect(GET(createEvent(GET))).rejects.toMatchObject({ status: 503 });
	});
});
