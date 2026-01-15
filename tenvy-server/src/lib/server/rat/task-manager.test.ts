import { describe, it, expect, vi, beforeEach } from 'vitest';
import { dispatchTaskManagerCommand, TaskManagerAgentError } from './task-manager';
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

describe('Task Manager', () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	it('should dispatch list command and return list', async () => {
		const agentId = 'agent-1';
		const commandId = 'cmd-list';
		const request = { operation: 'list' } as const;
		const responsePayload = {
			operation: 'list',
			status: 'ok',
			result: {
				processes: [],
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

		const result = await dispatchTaskManagerCommand(agentId, request);
		expect(result).toEqual(responsePayload.result);
	});

	it('should dispatch action command and return confirmation', async () => {
		const agentId = 'agent-1';
		const commandId = 'cmd-action';
		const request = { operation: 'action', pid: 1234, action: 'stop' } as const;
		const responsePayload = {
			operation: 'action',
			status: 'ok',
			result: {
				pid: 1234,
				action: 'stop',
				status: 'ok'
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

		const result = await dispatchTaskManagerCommand(agentId, request);
		expect(result.status).toBe('ok');
	});

	it('should handle timeout', async () => {
		vi.useFakeTimers();
		const agentId = 'agent-1';
		const commandId = 'cmd-timeout';
		
		mockRegistry.queueCommand.mockReturnValue({ command: { id: commandId } });
		mockRegistry.getAgent.mockReturnValue({ recentResults: [] });

		const promise = dispatchTaskManagerCommand(agentId, { operation: 'list' }, { timeoutMs: 1000 });
		const validation = expect(promise).rejects.toThrow('Timed out waiting');

		await vi.advanceTimersByTimeAsync(16000); // DEFAULT_TIMEOUT_MS is 15000

		await validation;
		vi.useRealTimers();
	});
});
