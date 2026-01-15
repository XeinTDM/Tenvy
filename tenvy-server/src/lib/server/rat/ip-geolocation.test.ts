import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { dispatchGeoCommand, GeoLookupAgentError } from './ip-geolocation';
import { registry, RegistryError } from './store';

vi.mock('./store', () => ({
	registry: {
		queueCommand: vi.fn(),
		getAgent: vi.fn()
	},
	RegistryError: class extends Error {
		status: number;
		constructor(message: string, status: number) {
			super(message);
			this.status = status;
		}
	}
}));

const mockRegistry = registry as unknown as {
	queueCommand: ReturnType<typeof vi.fn>;
	getAgent: ReturnType<typeof vi.fn>;
};

describe('IP Geolocation', () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	it('should dispatch command and return result on success', async () => {
		const agentId = 'agent-1';
		const commandId = 'cmd-1';
		const request = { action: 'status' };
		const responsePayload = {
			action: 'status',
			status: 'ok',
			result: {
				lastLookup: null,
				providers: ['ipinfo'],
				defaultProvider: 'ipinfo',
				generatedAt: new Date().toISOString()
			}
		};

		mockRegistry.queueCommand.mockReturnValue({ command: { id: commandId } });

		// Mock getAgent to return result immediately (or on first call)
		mockRegistry.getAgent.mockReturnValue({
			recentResults: [
				{
					commandId,
					success: true,
					output: JSON.stringify(responsePayload),
					completedAt: new Date().toISOString()
				}
			]
		});

		const result = await dispatchGeoCommand(agentId, request as any);

		expect(mockRegistry.queueCommand).toHaveBeenCalledWith(
			agentId,
			expect.objectContaining({ name: 'ip-geolocation' }),
			expect.any(Object)
		);
		expect(result).toEqual(responsePayload.result);
	});

	it('should handle agent error result', async () => {
		const agentId = 'agent-1';
		const commandId = 'cmd-1';

		mockRegistry.queueCommand.mockReturnValue({ command: { id: commandId } });
		mockRegistry.getAgent.mockReturnValue({
			recentResults: [
				{
					commandId,
					success: false,
					error: 'Some error',
					completedAt: new Date().toISOString()
				}
			]
		});

		await expect(dispatchGeoCommand(agentId, { action: 'status' } as any))
			.rejects.toThrow('Some error');
	});

	it('should handle malformed JSON response', async () => {
		const agentId = 'agent-1';
		const commandId = 'cmd-1';

		mockRegistry.queueCommand.mockReturnValue({ command: { id: commandId } });
		mockRegistry.getAgent.mockReturnValue({
			recentResults: [
				{
					commandId,
					success: true,
					output: '{ invalid json',
					completedAt: new Date().toISOString()
				}
			]
		});

		await expect(dispatchGeoCommand(agentId, { action: 'status' } as any))
			.rejects.toThrow('Geolocation response payload malformed');
	});

	it('should timeout if result never appears', async () => {
		vi.useFakeTimers();
		const agentId = 'agent-1';
		const commandId = 'cmd-1';

		mockRegistry.queueCommand.mockReturnValue({ command: { id: commandId } });
		mockRegistry.getAgent.mockReturnValue({ recentResults: [] });

		// STATUS_TIMEOUT_MS is 6000, which is the baseline.
		const promise = dispatchGeoCommand(agentId, { action: 'status' } as any, { timeoutMs: 1000 });

		const validation = expect(promise).rejects.toThrow('Timed out waiting');

		// Advance time past timeout (needs to be > 6000)
		await vi.advanceTimersByTimeAsync(7000);

		await validation;
		vi.useRealTimers();
	});

    it('should throw if registry throws', async () => {
        mockRegistry.queueCommand.mockImplementation(() => {
            throw new RegistryError('Agent not found', 404);
        });

        await expect(dispatchGeoCommand('agent-1', { action: 'status' } as any))
            .rejects.toThrow('Agent not found');
    });
});
