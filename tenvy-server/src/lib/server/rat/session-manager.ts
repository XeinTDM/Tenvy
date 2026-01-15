import type { AgentRecord } from './types';
import type { AgentCommandEnvelope, Command } from '../../../../../shared/types/messages';
import { EncryptionManager } from './encryption';

const SOCKET_OPEN_STATE = (() => {
	const globalSocket = (globalThis as { WebSocket?: { OPEN?: number } }).WebSocket;
	if (globalSocket && typeof globalSocket.OPEN === 'number') {
		return globalSocket.OPEN;
	}
	return 1;
})();

export class SessionManager {
	private readonly encryptionManager = new EncryptionManager();

	deliverViaSession(record: AgentRecord, command: Command): boolean {
		const session = record.session;
		if (!session) {
			return false;
		}

		const socket = session.socket;
		if (!socket || (socket.readyState ?? 0) !== SOCKET_OPEN_STATE) {
			return false;
		}

		try {
			const envelope: AgentCommandEnvelope = { type: 'command', command };
			
			if (record.sharedSecret) {
				const payload = Buffer.from(JSON.stringify(envelope), 'utf8');
				const encrypted = this.encryptionManager.encrypt(payload, record.sharedSecret);
				const encryptedEnvelope = {
					type: 'encrypted',
					data: encrypted.toString('base64')
				};
				socket.send(JSON.stringify(encryptedEnvelope));
			} else {
				socket.send(JSON.stringify(envelope));
			}
			
			return true;
		} catch {
			return false;
		}
	}

	detachSession(
		record: AgentRecord,
		sessionId: symbol,
		options: { close?: boolean; code?: number; reason?: string } = {}
	) {
		const session = record.session;
		if (!session || session.id !== sessionId) {
			return;
		}

		record.session = undefined;

		if (options.close === false) {
			return;
		}

		try {
			session.socket.close(options.code ?? 1000, options.reason);
		} catch {
			// Ignore close failures.
		}
	}
}
