import { describe, it, expect, vi, beforeEach } from 'vitest';
import { dispatchRegistryCommand, RegistryAgentError } from './registry';
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

describe('Registry Manager', () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	it('should dispatch list command', async () => {
		const agentId = 'agent-1';
		const commandId = 'cmd-reg-list';
		const request = { operation: 'list', hive: 'HKEY_CURRENT_USER', path: 'Software' } as const;
		const responsePayload = {
			operation: 'list',
			status: 'ok',
			result: {
				snapshot: {
                    'HKEY_CURRENT_USER': {
                        'Software': {
                            hive: 'HKEY_CURRENT_USER',
                            name: 'Software',
                            path: 'Software',
                            parentPath: null,
                            values: [],
                            subKeys: [],
                            lastModified: new Date().toISOString(),
                            wow64Mirrored: false,
                            owner: 'system'
                        }
                    },
                    'HKEY_LOCAL_MACHINE': {},
                    'HKEY_USERS': {}
                },
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

		const result = await dispatchRegistryCommand(agentId, request);
		expect(result).toEqual(responsePayload.result);
	});

	it('should handle mutation command', async () => {
		const agentId = 'agent-1';
		const commandId = 'cmd-reg-set';
		const request = { 
            operation: 'create', 
            target: 'value',
            hive: 'HKEY_CURRENT_USER', 
            keyPath: 'Software\\Test', 
            value: {
                name: 'Val', 
                data: 'Hello', 
                type: 'REG_SZ' 
            }
        } as const;
		const responsePayload = {
			operation: 'create',
			status: 'ok',
			result: {
				hive: {},
				keyPath: 'Software\\Test',
                valueName: 'Val',
                mutatedAt: new Date().toISOString()
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

		const result = await dispatchRegistryCommand(agentId, request);
		expect(result.keyPath).toBe('Software\\Test');
	});
});
