import { createHash, createHmac, randomUUID } from 'crypto';
import { sign } from 'tweetnacl';
import { db } from '$lib/server/db';
import { auditEvent as auditEventTable } from '$lib/server/db/schema';
import { eq, and } from 'drizzle-orm';
import type {
	Command,
	CommandResult,
	CommandQueueAuditRecord,
	CommandAcknowledgementRecord
} from '../../../../../shared/types/messages';
import type { AgentRecord } from './types';

export class CommandManager {
	signCommand(command: Command): string | undefined {
		const privateKeyHex = process.env.TENVY_COMMAND_PRIVATE_KEY;
		const secret = process.env.TENVY_COMMAND_SECRET;

		if (!privateKeyHex && !secret) {
			return undefined;
		}

		try {
			const payloadString = command.payload ? JSON.stringify(command.payload) : '';
			const data = [command.id, command.name, payloadString, command.createdAt].join('|');

			if (privateKeyHex) {
				// Ed25519 signing using tweetnacl
				const privateKey = Buffer.from(privateKeyHex, 'hex');
				// tweetnacl expects a 64-byte secret key (seed + public key)
				// Node's crypto usually provides just the 32-byte seed.
				// If it's 32 bytes, we need to expand it.
				let fullKey = privateKey;
				if (privateKey.length === 32) {
					fullKey = sign.keyPair.fromSeed(privateKey).secretKey;
				}
				const signature = sign.detached(Buffer.from(data), fullKey);
				return `ed25519:${Buffer.from(signature).toString('hex')}`;
			} else if (secret) {
				// Fallback to HMAC
				const hmac = createHmac('sha256', secret);
				hmac.update(data);
				return `hmac:${hmac.digest('hex')}`;
			}

			return undefined;
		} catch (error) {
			console.error('Failed to sign command', error);
			return undefined;
		}
	}

	logCommandQueued(
		record: AgentRecord,
		command: Command,
		operatorId?: string,
		acknowledgement?: CommandAcknowledgementRecord | null
	): CommandQueueAuditRecord | null {
		const payloadHash = this.hashCommandPayload(command.payload);
		const sanitizedAck = this.sanitizeAcknowledgement(acknowledgement);
		const acknowledgedAt = sanitizedAck ? new Date(sanitizedAck.confirmedAt) : null;
		const acknowledgementJson = sanitizedAck ? JSON.stringify(sanitizedAck) : null;

		try {
			db.insert(auditEventTable)
				.values({
					commandId: command.id,
					agentId: record.id,
					operatorId: operatorId ?? null,
					commandName: command.name,
					payloadHash,
					queuedAt: new Date(command.createdAt),
					acknowledgedAt,
					acknowledgement: acknowledgementJson
				})
				.onConflictDoUpdate({
					target: auditEventTable.commandId,
					set: {
						agentId: record.id,
						operatorId: operatorId ?? null,
						commandName: command.name,
						payloadHash,
						queuedAt: new Date(command.createdAt),
						acknowledgedAt,
						acknowledgement: acknowledgementJson
					}
				})
				.run();

			const row = db
				.select({
					id: auditEventTable.id,
					acknowledgedAt: auditEventTable.acknowledgedAt,
					acknowledgement: auditEventTable.acknowledgement
				})
				.from(auditEventTable)
				.where(eq(auditEventTable.commandId, command.id))
				.get();

			if (row) {
				return {
					eventId: typeof row.id === 'number' ? row.id : null,
					acknowledgedAt:
						row.acknowledgedAt instanceof Date ? row.acknowledgedAt.toISOString() : null,
					acknowledgement: this.deserializeAcknowledgement(row.acknowledgement)
				} satisfies CommandQueueAuditRecord;
			}
		} catch (error) {
			console.error('Failed to record command audit event', error);
		}

		if (sanitizedAck) {
			return {
				eventId: null,
				acknowledgedAt: acknowledgedAt ? acknowledgedAt.toISOString() : null,
				acknowledgement: sanitizedAck
			} satisfies CommandQueueAuditRecord;
		}

		return null;
	}

	logCommandExecuted(agentId: string, result: CommandResult): void {
		try {
			db.update(auditEventTable)
				.set({
					executedAt: new Date(result.completedAt),
					result: JSON.stringify({
						success: result.success,
						output: result.output ?? null,
						error: result.error ?? null
					})
				})
				.where(
					and(eq(auditEventTable.commandId, result.commandId), eq(auditEventTable.agentId, agentId))
				)
				.run();
		} catch (error) {
			console.error('Failed to record command execution audit event', error);
		}
	}

	private hashCommandPayload(payload: Command['payload']): string {
		const hash = createHash('sha256');
		try {
			const serialized = JSON.stringify(payload ?? {});
			hash.update(serialized, 'utf-8');
		} catch {
			hash.update('unserializable', 'utf-8');
		}
		return hash.digest('hex');
	}

	private sanitizeAcknowledgement(
		input: CommandAcknowledgementRecord | null | undefined
	): CommandAcknowledgementRecord | null {
		if (!input || typeof input !== 'object') {
			return null;
		}

		const rawTimestamp = typeof input.confirmedAt === 'string' ? input.confirmedAt.trim() : '';
		const statementsSource = Array.isArray(input.statements) ? input.statements : [];

		const statements = statementsSource
			.map((statement) => {
				if (!statement || typeof statement !== 'object') {
					return null;
				}
				const id =
					typeof (statement as { id?: unknown }).id === 'string'
						? (statement as { id: string }).id.trim()
						: '';
				const text =
					typeof (statement as { text?: unknown }).text === 'string'
						? (statement as { text: string }).text.trim()
						: '';
				if (!id || !text) {
					return null;
				}
				return { id, text };
			})
			.filter((entry): entry is { id: string; text: string } => Boolean(entry));

		if (statements.length === 0) {
			return null;
		}

		const parsedTimestamp = rawTimestamp ? new Date(rawTimestamp) : new Date();
		const confirmedAt = Number.isNaN(parsedTimestamp.getTime())
			? new Date().toISOString()
			: parsedTimestamp.toISOString();

		return { confirmedAt, statements };
	}

	private deserializeAcknowledgement(value: string | null): CommandAcknowledgementRecord | null {
		if (!value) {
			return null;
		}

		try {
			const parsed = JSON.parse(value) as CommandAcknowledgementRecord;
			return this.sanitizeAcknowledgement(parsed);
		} catch {
			return null;
		}
	}
}