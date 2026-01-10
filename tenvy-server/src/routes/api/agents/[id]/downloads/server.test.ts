import { describe, expect, it, beforeEach, afterEach, beforeAll, vi } from 'vitest';

vi.mock('$env/dynamic/private', () => import('../../../../../../tests/mocks/env-dynamic-private'));

let getHandler: typeof import('./+server').GET;
let registry: typeof import('$lib/server/rat/store').registry;
let RegistryErrorCtor: typeof import('$lib/server/rat/store').RegistryError;

beforeAll(async () => {
	({ GET: getHandler } = await import('./+server'));
	({ registry, RegistryError: RegistryErrorCtor } = await import('$lib/server/rat/store'));
});

function createEvent(agentId: string, userRole: 'viewer' | 'operator' = 'viewer') {
	return {
		params: { id: agentId },
		locals: {
			user: {
				id: 'user-1',
				role: userRole
			}
		}
	} as unknown as any; // eslint-disable-line @typescript-eslint/no-explicit-any
}

describe('/api/agents/[id]/downloads', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
	});

	afterEach(() => {
		vi.restoreAllMocks();
	});

	it('returns the downloads catalogue for the requested agent', async () => {
		const spy = vi.spyOn(registry, 'getDownloadsCatalogue').mockReturnValue([
			{
				id: 'atlas.exe',
				displayName: 'Atlas Explorer',
				version: '2.3.1',
				description: 'Reconnaissance utility'
			}
		]);

		const response = await getHandler(createEvent('agent-123'));
		expect(spy).toHaveBeenCalledWith('agent-123');
		expect(response.status).toBe(200);
		const payload = await response.json();
		expect(payload).toEqual({
			downloads: [
				{
					id: 'atlas.exe',
					displayName: 'Atlas Explorer',
					version: '2.3.1',
					description: 'Reconnaissance utility'
				}
			]
		});
	});

	it('returns an empty array when no downloads are available', async () => {
		vi.spyOn(registry, 'getDownloadsCatalogue').mockReturnValue([]);

		const response = await getHandler(createEvent('agent-empty'));
		expect(response.status).toBe(200);
		const payload = await response.json();
		expect(payload).toEqual({ downloads: [] });
	});

	it('propagates registry errors as HTTP errors', async () => {
		vi.spyOn(registry, 'getDownloadsCatalogue').mockImplementation(() => {
			throw new RegistryErrorCtor('Agent not found', 404);
		});

		try {
			getHandler(createEvent('missing'));
			throw new Error('Expected handler to throw');
		} catch (error) {
			const err = error as { status?: number; body?: { message?: string } };
			expect(err.status).toBe(404);
			expect(err.body?.message).toBe('Agent not found');
		}
	});
});
