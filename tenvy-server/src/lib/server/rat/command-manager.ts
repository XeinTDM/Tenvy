import { createHash, createHmac, randomUUID } from 'crypto';
import { sign } from 'tweetnacl';
import { db } from '$lib/server/db';
import { auditEvent as auditEventTable } from '$lib/server/db/schema';
import { eq, and } from 'drizzle-orm';
import type {
	Command,
	CommandResult,
	CommandQueueAuditRecord,
	CommandAcknowledgementRecord,
	CommandOutputEvent
} from '../../../../../shared/types/messages';
import type { AgentRecord, CommandOutputStreamRecord } from './types';
import {
	hashCommandPayload,
	sanitizeAcknowledgement,
	deserializeAcknowledgement,
	normalizeCommandOutputEvent,
	COMMAND_OUTPUT_RETENTION_MS
} from './utils';

export interface CommandOutputSubscription {
	events: CommandOutputEvent[];
	completed: boolean;
	unsubscribe: () => void;
}

export class CommandManager {
	private readonly commandOutputStreams = new Map<string, Map<string, CommandOutputStreamRecord>>();

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
				const privateKey = Buffer.from(privateKeyHex, 'hex');
				let fullKey: Uint8Array = privateKey;
				if (privateKey.length === 32) {
					fullKey = sign.keyPair.fromSeed(privateKey).secretKey;
				}
				const signature = sign.detached(Buffer.from(data), fullKey);
				return `ed25519:${Buffer.from(signature).toString('hex')}`;
			} else if (secret) {
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
		const payloadHash = hashCommandPayload(command.payload);
		const sanitizedAck = sanitizeAcknowledgement(acknowledgement);
		const acknowledgedAt = sanitizedAck ? new Date(sanitizedAck.confirmedAt) : null;
		const acknowledgementJson = sanitizedAck ? JSON.stringify(sanitizedAck) : null;

		try {
			const row = db
				.insert(auditEventTable)
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
				.returning({
					id: auditEventTable.id,
					acknowledgedAt: auditEventTable.acknowledgedAt,
					acknowledgement: auditEventTable.acknowledgement
				})
				.get();

			if (row) {
				return {
					eventId: typeof row.id === 'number' ? row.id : null,
					acknowledgedAt:
						row.acknowledgedAt instanceof Date ? row.acknowledgedAt.toISOString() : null,
					acknowledgement: deserializeAcknowledgement(row.acknowledgement)
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

	recordOutput(agentId: string, commandId: string, event: CommandOutputEvent): void {
		const stream = this.getCommandOutputStream(agentId, commandId, true);
		if (!stream) return;

		const normalized = normalizeCommandOutputEvent(commandId, event);
		stream.events.push(normalized);

		for (const listener of stream.listeners) {
			try {
				listener({ ...normalized });
			} catch (err) {
				console.error('Command output listener failed', err);
			}
		}

		if (normalized.type === 'end') {
			stream.completed = true;
		}

		this.scheduleCommandOutputCleanup(agentId, commandId, stream);
	}

	subscribeOutput(
		agentId: string,
		commandId: string,
		listener: (event: CommandOutputEvent) => void
	): CommandOutputSubscription | null {
		const stream = this.getCommandOutputStream(agentId, commandId, true);
		if (!stream) return null;

		this.clearCommandOutputCleanup(stream);
		stream.listeners.add(listener);
		this.scheduleCommandOutputCleanup(agentId, commandId, stream);

		const unsubscribe = () => {
			stream.listeners.delete(listener);
			if (stream.completed && stream.listeners.size === 0) {
				this.scheduleCommandOutputCleanup(agentId, commandId, stream);
			}
		};

		return {
			events: stream.events.map((e) => ({ ...e })),
			completed: stream.completed,
			unsubscribe
		};
	}

	private getCommandOutputStream(
		agentId: string,
		commandId: string,
		create: boolean
	): CommandOutputStreamRecord | null {
		let streams = this.commandOutputStreams.get(agentId);
		if (!streams) {
			if (!create) return null;
			streams = new Map();
			this.commandOutputStreams.set(agentId, streams);
		}

		let stream = streams.get(commandId);
		if (!stream && create) {
			stream = { events: [], listeners: new Set(), completed: false };
			streams.set(commandId, stream);
		}

		return stream ?? null;
	}

	private clearCommandOutputCleanup(stream: CommandOutputStreamRecord): void {
		if (stream.timeout) {
			clearTimeout(stream.timeout);
			stream.timeout = undefined;
		}
	}

	private scheduleCommandOutputCleanup(
		agentId: string,
		commandId: string,
		stream: CommandOutputStreamRecord
	): void {
		this.clearCommandOutputCleanup(stream);
		stream.timeout = setTimeout(() => {
			const streams = this.commandOutputStreams.get(agentId);
			if (!streams) return;
			const target = streams.get(commandId);
			if (!target) return;
			if (target.listeners.size > 0) {
				this.scheduleCommandOutputCleanup(agentId, commandId, target);
				return;
			}
			streams.delete(commandId);
			if (streams.size === 0) {
				this.commandOutputStreams.delete(agentId);
			}
		}, COMMAND_OUTPUT_RETENTION_MS);
	}
}