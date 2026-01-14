import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { CommandManager } from './command-manager';
import type { Command } from '../../../../../shared/types/messages';
import { generateKeyPairSync, createPublicKey } from 'crypto';

describe('CommandManager', () => {
	let commandManager: CommandManager;

	beforeEach(() => {
		commandManager = new CommandManager();
		process.env.TENVY_COMMAND_SECRET = 'test-secret';
		delete process.env.TENVY_COMMAND_PRIVATE_KEY;
	});

	afterEach(() => {
		delete process.env.TENVY_COMMAND_SECRET;
		delete process.env.TENVY_COMMAND_PRIVATE_KEY;
	});

	const mockCommand: Command = {
		id: 'cmd-1',
		name: 'ping',
		payload: { message: 'hi' },
		createdAt: '2026-01-13T00:00:00.000Z'
	};

	it('signs with HMAC when only secret is provided', () => {
		const signature = commandManager.signCommand(mockCommand);
		expect(signature).toBeDefined();
		expect(signature?.startsWith('hmac:')).toBe(true);
	});

	it('signs with ED25519 when private key is provided', () => {
		const { privateKey } = generateKeyPairSync('ed25519');
		const privateKeyHex = privateKey.export({ type: 'pkcs8', format: 'der' }).slice(-32).toString('hex');
		process.env.TENVY_COMMAND_PRIVATE_KEY = privateKeyHex;

		const signature = commandManager.signCommand(mockCommand);
		expect(signature).toBeDefined();
		expect(signature?.startsWith('ed25519:')).toBe(true);
	});

	it('returns undefined when no secret or private key is provided', () => {
		delete process.env.TENVY_COMMAND_SECRET;
		const signature = commandManager.signCommand(mockCommand);
		expect(signature).toBeUndefined();
	});
});
