import { describe, it, expect, vi, beforeEach } from 'vitest';
import { dispatchStartupCommand, StartupManagerAgentError } from './startup-manager';
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

describe('Startup Manager', () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	it('should dispatch list command and return inventory', async () => {
		const agentId = 'agent-1';
		const commandId = 'cmd-list';
		const request = { operation: 'list' } as const;
		const responsePayload = {
			operation: 'list',
			status: 'ok',
			result: {
				entries: [],
				generatedAt: new Date().toISOString()
			}
		};

		mockRegistry.queueCommand.mockReturnValue({ command: { id: commandId } });
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

		const result = await dispatchStartupCommand(agentId, request);

		expect(mockRegistry.queueCommand).toHaveBeenCalledWith(
			agentId,
			{ name: 'startup-manager', payload: { request } },
			expect.any(Object)
		);
		expect(result).toEqual(responsePayload.result);
	});

	it('should dispatch toggle command and return updated entry', async () => {
		const agentId = 'agent-1';
		const commandId = 'cmd-toggle';
		const request = { operation: 'toggle', entryId: 'e1', enabled: true } as const;
		const responsePayload = {
			operation: 'toggle',
			status: 'ok',
			result: {
				id: 'e1',
				name: 'Test',
				path: 'C:/test.exe',
				enabled: true,
				scope: 'user',
				source: 'registry',
				impact: 'low',
				location: 'HKCU',
				startupTime: 100,
				lastEvaluatedAt: new Date().toISOString()
			}
		};

		mockRegistry.queueCommand.mockReturnValue({ command: { id: commandId } });
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

		const result = await dispatchStartupCommand(agentId, request);

		expect(result).toEqual(responsePayload.result);
	});

	it('should handle agent error status', async () => {
		const agentId = 'agent-1';
		const commandId = 'cmd-err';
		const request = { operation: 'list' } as const;
		const responsePayload = {
			operation: 'list',
			status: 'error',
			error: 'Access denied',
			code: 'E_ACCESS'
		};

		mockRegistry.queueCommand.mockReturnValue({ command: { id: commandId } });
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

		await expect(dispatchStartupCommand(agentId, request))
			.rejects.toThrow('Access denied');
	});

	it('should timeout if no response', async () => {
		vi.useFakeTimers();
		const agentId = 'agent-1';
		const commandId = 'cmd-timeout';

		mockRegistry.queueCommand.mockReturnValue({ command: { id: commandId } });
		mockRegistry.getAgent.mockReturnValue({ recentResults: [] });

		const promise = dispatchStartupCommand(agentId, { operation: 'list' }, { timeoutMs: 1000 });
		const validation = expect(promise).rejects.toThrow('Timed out waiting');

		await vi.advanceTimersByTimeAsync(21000); // DEFAULT_TIMEOUT_MS is 20000

		await validation;
		vi.useRealTimers();
	});
});
