import { createHash, randomUUID } from 'crypto';
import { and, eq, inArray } from 'drizzle-orm';
import { db } from '$lib/server/db';
import {
	registrySubscription as registrySubscriptionTable,
	enrollmentToken as enrollmentTokenTable
} from '$lib/server/db/schema';
import {
	defaultAgentConfig,
	type AgentConfig,
} from '../../../../../shared/types/config';
import type { NoteEnvelope } from '../../../../../shared/types/notes';
import type { AgentRegistryEvent } from '../../../../../shared/types/registry-events';
import { COMMAND_STREAM_SUBPROTOCOL } from '../../../../../shared/constants/protocol';
import type {
	AgentMetadata,
	AgentMetrics,
	AgentSnapshot,
	AgentStatus,
	AgentOperatorNote
} from '../../../../../shared/types/agent';
import type {
	AgentRegistrationRequest,
	AgentRegistrationResponse
} from '../../../../../shared/types/auth';
import type {
	AgentControlCommandPayload,
	AgentSyncRequest,
	AgentSyncResponse,
	AgentCommandEnvelope,
	AgentRemoteDesktopInputEnvelope,
	AgentAppVncInputEnvelope,
	Command,
	CommandDeliveryMode,
	CommandInput,
	CommandQueueAuditRecord,
	CommandQueueResponse,
	CommandResult,
	CommandOutputEvent
} from '../../../../../shared/types/messages';
import type {
	OptionsState,
	OptionsScriptConfig,
	OptionsScriptFile,
	OptionsScriptRuntimeState
} from '../../../../../shared/types/options';
import type { RemoteDesktopInputBurst } from '../../../../../shared/types/remote-desktop';
import type { AppVncInputBurst } from '../../../../../shared/types/app-vnc';
import {
	downloadCatalogueSchema,
	type DownloadCatalogue,
	type DownloadCatalogueEntry
} from '$lib/types/downloads';
import { PluginTelemetryStore } from '../plugins/telemetry-store.js';
import { getAgentSignaturePolicy } from '../plugins/signature-policy.js';
import type {
	AgentRecord,
	OperatorNoteRecord,
	SharedNoteRecord,
	SessionTokenRecord
} from './types';
import { logger } from '../logger';
import { AgentPersistence } from './persistence';
import { CommandManager, type CommandOutputSubscription } from './command-manager';
import { SessionManager } from './session-manager';
import * as utils from './utils';

export const {
	MAX_TAGS,
	MAX_TAG_LENGTH,
	TAG_PATTERN,
	MAX_RECENT_RESULTS,
	MAX_PENDING_COMMANDS,
	PENDING_COMMAND_DROP_WARN_INTERVAL_MS,
	PERSIST_DEBOUNCE_MS,
	SESSION_TOKEN_TTL_MS,
	COMMAND_OUTPUT_RETENTION_MS,
	INACTIVITY_CHECK_INTERVAL_MS,
	INACTIVITY_TIMEOUT_MULTIPLIER,
	MIN_INACTIVITY_TIMEOUT_MS
} = utils;

const SOCKET_OPEN_STATE = (() => {
	const globalSocket = (globalThis as { WebSocket?: { OPEN?: number } }).WebSocket;
	if (globalSocket && typeof globalSocket.OPEN === 'number') {
		return globalSocket.OPEN;
	}
	return 1;
})();

class RegistryError extends Error {
	public readonly isRegistryError = true;
	constructor(
		message: string,
		public status: number = 400
	) {
		super(message);
		this.name = 'RegistryError';
	}
}

type AgentRegistrySubscriber = (event: AgentRegistryEvent) => void;

interface AdminSubscriptionRecord {
	id: string;
	adminId: string;
	channel: string;
	listener: AgentRegistrySubscriber;
	cursor: number;
}

interface PersistedAdminSubscription {
	id: string;
	adminId: string;
	channel: string;
	cursor: number;
	snapshot: AgentSnapshot[];
	lastSeenAt: Date;
	updatedAt: Date;
}

function normalizeSubscriptionSegment(value: string): string {
	return value.trim().toLowerCase();
}

function computeSubscriptionId(adminId: string, channel: string): string {
	const hash = createHash('sha256');
	hash.update(normalizeSubscriptionSegment(adminId));
	hash.update(':');
	hash.update(normalizeSubscriptionSegment(channel));
	return hash.digest('hex');
}

function parseSubscriptionSnapshot(payload: string | null): AgentSnapshot[] {
	if (!payload) {
		return [];
	}
	try {
		const parsed = JSON.parse(payload) as AgentSnapshot[];
		return Array.isArray(parsed) ? parsed : [];
	} catch {
		return [];
	}
}

class RegistrySubscriptionStore {
	load(adminId: string, channel: string): PersistedAdminSubscription | null {
		try {
			const row = db
				.select({
					id: registrySubscriptionTable.id,
					adminId: registrySubscriptionTable.adminId,
					channel: registrySubscriptionTable.channel,
					cursor: registrySubscriptionTable.cursor,
					snapshot: registrySubscriptionTable.snapshot,
					lastSeenAt: registrySubscriptionTable.lastSeenAt,
					updatedAt: registrySubscriptionTable.updatedAt
				})
				.from(registrySubscriptionTable)
				.where(
					and(
						eq(registrySubscriptionTable.adminId, normalizeSubscriptionSegment(adminId)),
						eq(registrySubscriptionTable.channel, normalizeSubscriptionSegment(channel))
					)
				)
				.get();

			if (!row) {
				return null;
			}

			const lastSeen =
				row.lastSeenAt instanceof Date ? row.lastSeenAt : new Date(row.lastSeenAt ?? Date.now());
			const updated =
				row.updatedAt instanceof Date ? row.updatedAt : new Date(row.updatedAt ?? Date.now());

			return {
				id: row.id,
				adminId: row.adminId,
				channel: row.channel,
				cursor: typeof row.cursor === 'number' ? row.cursor : 0,
				snapshot: parseSubscriptionSnapshot(row.snapshot ?? null),
				lastSeenAt: lastSeen,
				updatedAt: updated
			} satisfies PersistedAdminSubscription;
		} catch (error) {
			logger.error('Failed to load registry subscription', { adminId, channel }, error);
			return null;
		}
	}

	upsert(
		adminId: string,
		channel: string,
		snapshot: AgentSnapshot[],
		cursor: number
	): PersistedAdminSubscription | null {
		const normalizedAdmin = normalizeSubscriptionSegment(adminId);
		const normalizedChannel = normalizeSubscriptionSegment(channel);
		const id = computeSubscriptionId(normalizedAdmin, normalizedChannel);
		const now = new Date();

		try {
			db.insert(registrySubscriptionTable)
				.values({
					id,
					adminId: normalizedAdmin,
					channel: normalizedChannel,
					cursor,
					snapshot: JSON.stringify(snapshot ?? []),
					createdAt: now,
					lastSeenAt: now,
					updatedAt: now
				})
				.onConflictDoUpdate({
					target: [registrySubscriptionTable.adminId, registrySubscriptionTable.channel],
					set: {
						cursor,
						snapshot: JSON.stringify(snapshot ?? []),
						lastSeenAt: now,
						updatedAt: now
					}
				})
				.run();
		} catch (error) {
			logger.error('Failed to persist registry subscription', { adminId, channel }, error);
			return null;
		}

		return this.load(normalizedAdmin, normalizedChannel);
	}

	updateCursor(adminId: string, channel: string, cursor: number): void {
		const normalizedAdmin = normalizeSubscriptionSegment(adminId);
		const normalizedChannel = normalizeSubscriptionSegment(channel);
		try {
			db.update(registrySubscriptionTable)
				.set({
					cursor,
					lastSeenAt: new Date()
				})
				.where(
					and(
						eq(registrySubscriptionTable.adminId, normalizedAdmin),
						eq(registrySubscriptionTable.channel, normalizedChannel)
					)
				)
				.run();
		} catch (error) {
			logger.error('Failed to update registry subscription cursor', { adminId, channel }, error);
		}
	}

	updateSnapshot(
		adminId: string,
		channel: string,
		snapshot: AgentSnapshot[],
		cursor: number
	): void {
		const normalizedAdmin = normalizeSubscriptionSegment(adminId);
		const normalizedChannel = normalizeSubscriptionSegment(channel);
		try {
			db.update(registrySubscriptionTable)
				.set({
					cursor,
					snapshot: JSON.stringify(snapshot ?? []),
					updatedAt: new Date(),
					lastSeenAt: new Date()
				})
				.where(
					and(
						eq(registrySubscriptionTable.adminId, normalizedAdmin),
						eq(registrySubscriptionTable.channel, normalizedChannel)
					)
				)
				.run();
		} catch (error) {
			logger.error('Failed to update registry subscription snapshot', { adminId, channel }, error);
		}
	}

	touch(adminId: string, channel: string): void {
		const normalizedAdmin = normalizeSubscriptionSegment(adminId);
		const normalizedChannel = normalizeSubscriptionSegment(channel);
		try {
			db.update(registrySubscriptionTable)
				.set({ lastSeenAt: new Date() })
				.where(
					and(
						eq(registrySubscriptionTable.adminId, normalizedAdmin),
						eq(registrySubscriptionTable.channel, normalizedChannel)
					)
				)
				.run();
		} catch (error) {
			logger.error(
				'Failed to update registry subscription activity timestamp',
				{ adminId, channel },
				error
			);
		}
	}
}

function cloneOptionsFile(
	file: OptionsScriptFile | null | undefined
): OptionsScriptFile | null | undefined {
	return utils.cloneOptionsFile(file);
}

function cloneOptionsConfig(
	config: OptionsScriptConfig | null | undefined
): OptionsScriptConfig | null | undefined {
	return utils.cloneOptionsConfig(config);
}

function cloneOptionsRuntime(
	runtime: OptionsScriptRuntimeState | null | undefined
): OptionsScriptRuntimeState | null | undefined {
	return utils.cloneOptionsRuntime(runtime);
}

function cloneOptionsState(state: OptionsState | null | undefined): OptionsState | null {
	return utils.cloneOptionsState(state);
}

function generateAgentKey(): { token: string; hash: string } {
	return utils.generateAgentKey();
}

function generateSessionToken(): { token: string; hash: string; expiresAt: number } {
	return utils.generateSessionToken(SESSION_TOKEN_TTL_MS);
}

export class AgentRegistry {
	private readonly agents = new Map<string, AgentRecord>();
	private readonly fingerprints = new Map<string, string>();
	private readonly sessionTokens = new Map<string, SessionTokenRecord>();
	private readonly subscribers = new Map<string, AgentRegistrySubscriber>();
	private readonly adminSubscriptions = new Map<string, AdminSubscriptionRecord>();
	private persistTimer: ReturnType<typeof setTimeout> | null = null;
	private persistPromise: Promise<void> | null = null;
	private needsPersist = false;
	private broadcastSequence = 0;
	private readonly pluginTelemetry: PluginTelemetryStore;
	private readonly subscriptionStore = new RegistrySubscriptionStore();
	private readonly persistence = new AgentPersistence();
	private readonly commandManager = new CommandManager();
	private readonly sessionManager = new SessionManager();
	private inactivityCheckTimer: ReturnType<typeof setInterval> | null = null;

	constructor() {
		this.loadInitialState();
		this.pluginTelemetry = new PluginTelemetryStore();
		this.startInactivityMonitor();
	}

	private loadInitialState(): void {
		const records = this.persistence.loadAllAgents();
		for (const record of records) {
			this.agents.set(record.id, record);
			this.fingerprints.set(record.fingerprint, record.id);
		}
	}

	private startInactivityMonitor(): void {
		if (this.inactivityCheckTimer) {
			return;
		}

		const timer = setInterval(() => {
			try {
				this.pruneInactiveAgents();
			} catch (error) {
				console.error('Failed to prune inactive agents', error);
			}
		}, INACTIVITY_CHECK_INTERVAL_MS);

		timer.unref?.();
		this.inactivityCheckTimer = timer;
	}

	private markDirty(record: AgentRecord): void {
		record.dirty = true;
		this.schedulePersist();
	}

	private pruneInactiveAgents(): void {
		const now = Date.now();

		for (const record of this.agents.values()) {
			if (record.session) {
				continue;
			}

			const inactiveDuration = now - record.lastSeen.getTime();

			if (record.status === 'online') {
				const timeout = this.getAgentInactivityTimeout(record);
				if (inactiveDuration >= timeout) {
					record.status = 'offline';
					this.markDirty(record);
					this.notifyAgentUpdate(record);
				}
			}
		}
	}

	private getAgentInactivityTimeout(record: AgentRecord): number {
		const pollInterval = Math.max(record.config.pollIntervalMs, 1);
		const maxBackoff = Math.max(
			record.config.maxBackoffMs ?? defaultAgentConfig.maxBackoffMs,
			pollInterval
		);
		const base = Math.max(maxBackoff, MIN_INACTIVITY_TIMEOUT_MS);
		return base * INACTIVITY_TIMEOUT_MULTIPLIER;
	}

	subscribe(listener: AgentRegistrySubscriber): () => void {
		const id = randomUUID();
		this.subscribers.set(id, listener);
		return () => {
			this.subscribers.delete(id);
		};
	}

	subscribeForAdmin(
		adminId: string,
		listener: AgentRegistrySubscriber,
		options: { channel?: string } = {}
	): { unsubscribe: () => void; snapshot: AgentSnapshot[]; cursor: number } {
		const normalizedAdmin = normalizeSubscriptionSegment(adminId);
		const channel = normalizeSubscriptionSegment(options.channel ?? 'sse');
		const connectionId = `${computeSubscriptionId(normalizedAdmin, channel)}:${randomUUID()}`;

		const record: AdminSubscriptionRecord = {
			id: connectionId,
			adminId: normalizedAdmin,
			channel,
			listener,
			cursor: this.broadcastSequence
		};

		this.adminSubscriptions.set(connectionId, record);

		const currentSnapshot = this.listAgents();
		const persisted = this.subscriptionStore.upsert(
			normalizedAdmin,
			channel,
			currentSnapshot,
			record.cursor
		) ?? {
			id: computeSubscriptionId(normalizedAdmin, channel),
			adminId: normalizedAdmin,
			channel,
			cursor: record.cursor,
			snapshot: currentSnapshot,
			lastSeenAt: new Date(),
			updatedAt: new Date()
		};

		record.cursor = persisted.cursor;

		return {
			cursor: record.cursor,
			snapshot: persisted.snapshot.length > 0 ? persisted.snapshot : currentSnapshot,
			unsubscribe: () => {
				this.adminSubscriptions.delete(connectionId);
				this.subscriptionStore.touch(normalizedAdmin, channel);
			}
		};
	}

	getPersistedSubscriptionSnapshot(
		adminId: string,
		options: { channel?: string } = {}
	): AgentSnapshot[] {
		const normalizedAdmin = normalizeSubscriptionSegment(adminId);
		const channel = normalizeSubscriptionSegment(options.channel ?? 'sse');
		const persisted = this.subscriptionStore.load(normalizedAdmin, channel);
		if (persisted?.snapshot?.length) {
			return persisted.snapshot;
		}
		return this.listAgents();
	}

	private broadcast(event: AgentRegistryEvent): void {
		this.broadcastSequence += 1;
		const sequence = this.broadcastSequence;
		const shouldPersistSnapshot = event.type === 'agents' || event.type === 'agent';
		const snapshot = shouldPersistSnapshot
			? event.type === 'agents'
				? (event.agents ?? [])
				: this.listAgents()
			: null;

		for (const listener of this.subscribers.values()) {
			try {
				listener(event);
			} catch (error) {
				console.error('Agent registry subscriber failed', error);
			}
		}

		const uniqueSubscriptions = new Set<string>();

		for (const record of this.adminSubscriptions.values()) {
			try {
				record.listener(event);
			} catch (error) {
				console.error('Agent registry subscriber failed', error);
			}

			record.cursor = sequence;

			const subscriptionKey = `${record.adminId}:${record.channel}`;
			if (uniqueSubscriptions.has(subscriptionKey)) {
				continue;
			}
			uniqueSubscriptions.add(subscriptionKey);

			if (shouldPersistSnapshot && snapshot) {
				this.subscriptionStore.updateSnapshot(record.adminId, record.channel, snapshot, sequence);
			} else {
				this.subscriptionStore.updateCursor(record.adminId, record.channel, sequence);
			}
		}
	}

	private notifyAgentUpdate(record: AgentRecord): void {
		this.broadcast({ type: 'agent', agent: this.toSnapshot(record) });
	}

	private serializeSharedNotes(record: AgentRecord): NoteEnvelope[] {
		return Array.from(record.sharedNotes.values()).map(
			(note) =>
				({
					id: note.id,
					visibility: 'shared',
					ciphertext: note.ciphertext,
					nonce: note.nonce,
					digest: note.digest,
					version: note.version,
					updatedAt: note.updatedAt.toISOString()
				}) satisfies NoteEnvelope
		);
	}

	private notifyNotes(record: AgentRecord): void {
		this.broadcast({ type: 'notes', agentId: record.id, notes: this.serializeSharedNotes(record) });
	}

	private getAgentRecord(id: string): AgentRecord | undefined {
		const record = this.agents.get(id);
		if (record) {
			return record;
		}

		const loaded = this.persistence.loadAgentById(id);
		if (loaded) {
			this.agents.set(loaded.id, loaded);
			this.fingerprints.set(loaded.fingerprint, loaded.id);
			return loaded;
		}

		return undefined;
	}

	private getAgentRecordByFingerprint(fingerprint: string): AgentRecord | undefined {
		const id = this.fingerprints.get(fingerprint);
		if (id) {
			return this.getAgentRecord(id);
		}

		const loaded = this.persistence.loadAgentByFingerprint(fingerprint);
		if (loaded) {
			this.agents.set(loaded.id, loaded);
			this.fingerprints.set(loaded.fingerprint, loaded.id);
			return loaded;
		}

		return undefined;
	}

	private schedulePersist(): void {
		this.needsPersist = true;
		if (this.persistPromise) {
			return;
		}
		if (this.persistTimer) {
			return;
		}
		this.persistTimer = setTimeout(() => {
			this.persistTimer = null;
			this.persistPromise = this.flushPersistLoop();
		}, PERSIST_DEBOUNCE_MS);
	}

	private async flushPersistLoop(): Promise<void> {
		try {
			while (this.needsPersist) {
				this.needsPersist = false;
				try {
					await this.persistToDatabase();
				} catch (error) {
					console.error('Failed to persist agent registry', error);
				}
			}
		} finally {
			this.persistPromise = null;
		}
	}

	private async persistToDatabase(): Promise<void> {
		const agents = Array.from(this.agents.values()).filter((a) => a.dirty);
		if (agents.length === 0) {
			return;
		}

		await this.persistence.persistAgents(agents);
	}

	async flush(): Promise<void> {
		if (this.persistTimer) {
			clearTimeout(this.persistTimer);
			this.persistTimer = null;
		}

		if (!this.persistPromise && this.needsPersist) {
			this.persistPromise = this.flushPersistLoop();
		}

		if (this.persistPromise) {
			await this.persistPromise;
		}
	}

	private toSnapshot(record: AgentRecord): AgentSnapshot {
		return {
			id: record.id,
			metadata: { ...record.metadata, tags: Array.isArray(record.metadata.tags) ? [...record.metadata.tags] : undefined },
			status: record.status,
			connectedAt: record.connectedAt.toISOString(),
			lastSeen: record.lastSeen.toISOString(),
			metrics: record.metrics ? { ...record.metrics } : undefined,
			pendingCommands: record.pendingCommands.length,
			recentResults: record.recentResults.map((result) => ({ ...result })),
			liveSession: Boolean(record.session),
			operatorNote: record.operatorNote
				? ({
						note: record.operatorNote.note,
						tags: [...record.operatorNote.tags],
						updatedAt: record.operatorNote.updatedAt
							? record.operatorNote.updatedAt.toISOString()
							: null,
						updatedBy: record.operatorNote.updatedBy
					} satisfies AgentOperatorNote)
				: undefined
		} satisfies AgentSnapshot;
	}

	private detachSession(
		record: AgentRecord,
		sessionId: symbol,
		options: { close?: boolean; code?: number; reason?: string; markOffline?: boolean } = {}
	) {
		this.sessionManager.detachSession(record, sessionId, options);

		if (options.markOffline !== false && !record.session) {
			record.status = 'offline';
			record.lastSeen = new Date();
			this.schedulePersist();
			this.notifyAgentUpdate(record);
		}
	}

	private deliverViaSession(record: AgentRecord, command: Command): boolean {
		const delivered = this.sessionManager.deliverViaSession(record, command);
		if (!delivered && record.session) {
			this.detachSession(record, record.session.id, { close: false });
		}
		return delivered;
	}

	private clampPendingCommands(record: AgentRecord, dropFrom: 'front' | 'back' = 'front'): void {
		const overflow = record.pendingCommands.length - MAX_PENDING_COMMANDS;
		if (overflow <= 0) {
			return;
		}

		if (dropFrom === 'back') {
			record.pendingCommands.splice(record.pendingCommands.length - overflow, overflow);
			this.warnPendingCommandDrop(record, overflow, dropFrom);
			return;
		}

		record.pendingCommands.splice(0, overflow);
		this.warnPendingCommandDrop(record, overflow, dropFrom);
	}

	private warnPendingCommandDrop(
		record: AgentRecord,
		dropped: number,
		dropFrom: 'front' | 'back'
	): void {
		if (dropped <= 0) {
			return;
		}

		const now = Date.now();
		if (
			record.lastQueueDropWarning &&
			now - record.lastQueueDropWarning < PENDING_COMMAND_DROP_WARN_INTERVAL_MS
		) {
			return;
		}

		record.lastQueueDropWarning = now;
		const direction = dropFrom === 'front' ? 'oldest' : 'newest';
		const plural = dropped === 1 ? '' : 's';
		console.warn(
			`Pending command queue for agent ${record.id} reached capacity (${MAX_PENDING_COMMANDS}); dropped ${dropped} ${direction} command${plural}.`
		);
	}

	private validateToken(requestToken: string | undefined) {
		const expected = process.env.TENVY_SHARED_SECRET;
		if (expected && expected !== requestToken) {
			throw new RegistryError('Invalid registration token', 401);
		}

		if (expected && expected === requestToken) {
			return;
		}

		if (!requestToken) {
			// If no expected shared secret and no request token, we might allow it
			// depending on whether enrollment tokens are mandatory.
			// The original code allowed it if expected was not set.
			if (!expected) return;
			throw new RegistryError('Missing registration token', 401);
		}

		try {
			const result = db.transaction((tx) => {
				const row = tx
					.select()
					.from(enrollmentTokenTable)
					.where(eq(enrollmentTokenTable.token, requestToken))
					.get();

				if (!row) return false;
				if (row.revokedAt) return false;
				if (row.expiresAt && row.expiresAt < new Date()) return false;
				if (row.uses >= row.maxUses) return false;

				tx.update(enrollmentTokenTable)
					.set({ uses: row.uses + 1 })
					.where(eq(enrollmentTokenTable.token, requestToken))
					.run();
				return true;
			});

			if (!result) {
				throw new RegistryError('Invalid registration token', 401);
			}
		} catch (error) {
			if (error instanceof RegistryError) {
				throw error;
			}
			logger.error('Failed to validate enrollment token', { token: requestToken }, error);
			throw new RegistryError('Internal validation error', 500);
		}
	}

	registerAgent(
		payload: AgentRegistrationRequest,
		options: { remoteAddress?: string } = {}
	): AgentRegistrationResponse {
		this.validateToken(payload.token);
		const now = new Date();
		const normalizedTags = utils.normalizeTags(payload.metadata.tags ?? []);
		const incomingMetadata = utils.ensureMetadata(
			{ ...payload.metadata, tags: normalizedTags.length > 0 ? normalizedTags : undefined },
			options.remoteAddress
		);
		const fingerprint = utils.computeFingerprint(incomingMetadata);

		let serverPublicKey: string | undefined;
		let sharedSecret: string | undefined;

		if (payload.publicKey) {
			const serverKeys = utils.generateRawX25519KeyPair();
			serverPublicKey = serverKeys.publicKey;
			sharedSecret = utils.deriveRawSharedSecret(serverKeys.privateKey, payload.publicKey);
		}

		const existingRecord = this.getAgentRecordByFingerprint(fingerprint);
		if (existingRecord) {
			if (existingRecord.session) {
				this.detachSession(existingRecord, existingRecord.session.id, {
					code: 1012,
					reason: 'Registration superseded active session',
					markOffline: false
				});
			}

			const hasExplicitTags = Array.isArray(existingRecord.metadata.tags);
			const nextMetadata: AgentMetadata = utils.ensureMetadata(
				{
					...existingRecord.metadata,
					...incomingMetadata,
					tags: hasExplicitTags ? existingRecord.metadata.tags : incomingMetadata.tags
				},
				options.remoteAddress
			);

			const previousFingerprint = existingRecord.fingerprint;
			existingRecord.metadata = nextMetadata;
			existingRecord.status = 'online';
			existingRecord.connectedAt = now;
			existingRecord.lastSeen = now;
			existingRecord.metrics = undefined;
			const nextKey = utils.generateAgentKey();
			existingRecord.keyHash = nextKey.hash;
			existingRecord.config = utils.normalizeConfig(existingRecord.config);
			existingRecord.fingerprint = utils.computeFingerprint(nextMetadata);
			existingRecord.sharedSecret = sharedSecret;
			this.sessionTokens.delete(existingRecord.id);

			if (previousFingerprint !== existingRecord.fingerprint) {
				this.fingerprints.delete(previousFingerprint);
			}
			this.fingerprints.set(existingRecord.fingerprint, existingRecord.id);
			this.agents.set(existingRecord.id, existingRecord);
			this.markDirty(existingRecord);
			this.notifyAgentUpdate(existingRecord);

			return {
				agentId: existingRecord.id,
				agentKey: nextKey.token,
				config: { ...existingRecord.config },
				commands: [],
				serverTime: now.toISOString(),
				serverPublicKey
			};
		}

		this.fingerprints.delete(fingerprint);

		const id = randomUUID();
		const nextKey = utils.generateAgentKey();
		const record: AgentRecord = {
			id,
			keyHash: nextKey.hash,
			metadata: incomingMetadata,
			status: 'online',
			connectedAt: now,
			lastSeen: now,
			metrics: undefined,
			config: utils.normalizeConfig(null),
			pendingCommands: [],
			recentResults: [],
			sharedNotes: new Map(),
			operatorNote: null,
			fingerprint,
			sharedSecret,
			optionsState: null,
			downloadsCatalogue: []
		};

		this.agents.set(id, record);
		this.fingerprints.set(fingerprint, id);
		this.sessionTokens.delete(id);
		this.markDirty(record);
		this.notifyAgentUpdate(record);

		return {
			agentId: id,
			agentKey: nextKey.token,
			config: { ...record.config },
			commands: [],
			serverTime: now.toISOString(),
			serverPublicKey
		};
	}

	issueSessionToken(id: string, key: string | undefined): { token: string; expiresAt: string } {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		if (!this.verifyAgentKey(record, key)) {
			throw new RegistryError('Invalid agent key', 401);
		}

		const generated = utils.generateSessionToken(SESSION_TOKEN_TTL_MS);
		this.sessionTokens.set(id, { hash: generated.hash, expiresAt: generated.expiresAt });

		return {
			token: generated.token,
			expiresAt: new Date(generated.expiresAt).toISOString()
		};
	}

	attachSession(
		id: string,
		token: string | undefined,
		socket: WebSocket,
		options: { remoteAddress?: string } = {}
	): void {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		this.consumeSessionToken(record, token);

		const sessionId = Symbol(`agent:${id}`);

		if (record.session) {
			this.detachSession(record, record.session.id, {
				code: 1012,
				reason: 'Session replaced',
				markOffline: false
			});
		}

		record.lastSeen = new Date();
		record.status = 'online';

		const acceptingSocket = socket as unknown as {
			accept?: (options?: { protocol?: string }) => void;
		};
		if (typeof acceptingSocket.accept === 'function') {
			try {
				acceptingSocket.accept({ protocol: COMMAND_STREAM_SUBPROTOCOL });
			} catch {
				// Ignore accept failures; send will surface errors later.
			}
		}

		const closeListener = () => {
			this.detachSession(record, sessionId, { close: false });
		};

		if (typeof socket.addEventListener === 'function') {
			socket.addEventListener('close', closeListener);
			socket.addEventListener('error', closeListener);
		} else {
			// Bun exposes onclose/onerror; fall back to direct assignment when listeners are unavailable.
			(socket as unknown as { onclose?: () => void }).onclose = closeListener;
			(socket as unknown as { onerror?: () => void }).onerror = closeListener;
		}

		record.session = { id: sessionId, socket };

		if (options.remoteAddress) {
			record.metadata = utils.ensureMetadata(record.metadata, options.remoteAddress);
		}

		if (record.pendingCommands.length > 0) {
			const queued = record.pendingCommands;
			record.pendingCommands = [];
			for (let idx = 0; idx < queued.length; idx += 1) {
				const command = queued[idx];
				if (!this.deliverViaSession(record, command)) {
					record.pendingCommands = queued.slice(idx);
					this.clampPendingCommands(record, 'front');
					break;
				}
			}
		}

		this.markDirty(record);
		this.notifyAgentUpdate(record);
	}

	async syncAgent(
		id: string,
		key: string | undefined,
		payload: AgentSyncRequest,
		options: { remoteAddress?: string } = {}
	): Promise<AgentSyncResponse> {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		if (!this.verifyAgentKey(record, key)) {
			throw new RegistryError('Invalid agent key', 401);
		}

		record.lastSeen = new Date();
		record.status = payload.status;

		if (options.remoteAddress) {
			record.metadata = utils.ensureMetadata(record.metadata, options.remoteAddress);
		}
		if (payload.metrics) {
			record.metrics = { ...payload.metrics };
		}
		if (payload.results && payload.results.length > 0) {
			record.recentResults = utils.mergeRecentResults(record.recentResults, payload.results, MAX_RECENT_RESULTS);
			for (const result of payload.results) {
				this.commandManager.logCommandExecuted(record.id, result);
			}
		}

		if (payload.options !== undefined) {
			record.optionsState = utils.cloneOptionsState(payload.options);
		}

		const commands = record.pendingCommands.map((command) => ({ ...command }));
		record.pendingCommands = [];

		if (payload.plugins?.installations?.length) {
			await this.pluginTelemetry.syncAgent(
				record.id,
				record.metadata,
				payload.plugins.installations
			);
		}

		const manifestDelta = await this.pluginTelemetry.getAgentManifestDelta(
			record.id,
			payload.plugins?.manifests
		);

		this.markDirty(record);

		const optionsPayload = utils.cloneOptionsState(record.optionsState ?? null);

		this.notifyAgentUpdate(record);

		return {
			agentId: id,
			commands,
			config: { ...record.config },
			serverTime: new Date().toISOString(),
			pluginManifests: manifestDelta,
			options: optionsPayload
		};
	}

	async recordCommandOutput(
		id: string,
		commandId: string,
		key: string,
		event: CommandOutputEvent,
		options: { remoteAddress?: string } = {}
	): Promise<void> {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		if (!this.verifyAgentKey(record, key)) {
			throw new RegistryError('Invalid agent key', 401);
		}

		record.lastSeen = new Date();
		if (options.remoteAddress) {
			record.metadata = utils.ensureMetadata(record.metadata, options.remoteAddress);
		}

		this.commandManager.recordOutput(id, commandId, event);

		this.markDirty(record);
	}

	subscribeCommandOutput(
		id: string,
		commandId: string,
		listener: (event: CommandOutputEvent) => void
	): CommandOutputSubscription {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		const subscription = this.commandManager.subscribeOutput(id, commandId, listener);
		if (!subscription) {
			throw new RegistryError('Failed to create command output stream', 500);
		}

		return subscription;
	}

	queueCommand(
		id: string,
		input: CommandInput,
		options: { operatorId?: string; acknowledgement?: CommandAcknowledgementRecord | null } = {}
	): CommandQueueResponse {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		const command: Command = {
			id: randomUUID(),
			name: input.name,
			payload: input.payload,
			createdAt: new Date().toISOString()
		};

		command.signature = this.commandManager.signCommand(command);

		const audit = this.commandManager.logCommandQueued(
			record,
			command,
			options.operatorId,
			options.acknowledgement
		);

		const delivered = this.deliverViaSession(record, command);
		if (!delivered) {
			record.pendingCommands.push(command);
			this.clampPendingCommands(record, 'front');
		}

		this.markDirty(record);

		const delivery: CommandDeliveryMode = delivered ? 'session' : 'queued';
		this.notifyCommand(record, command, delivery);
		this.notifyAgentUpdate(record);
		return { command, delivery, audit: audit ?? null };
	}

	async requireAgentPluginVersion(
		agentId: string,
		pluginId: string,
		version: string
	): Promise<void> {
		const trimmedPluginId = pluginId.trim();
		if (trimmedPluginId.length === 0) {
			return;
		}

		const record = await this.pluginTelemetry.getAgentPlugin(agentId, trimmedPluginId);
		if (!record) {
			throw new RegistryError('Remote desktop engine plugin is not installed', 409);
		}

		if (!record.enabled) {
			throw new RegistryError('Remote desktop engine plugin is disabled', 409);
		}

		if (record.status !== 'installed') {
			const reason = record.error?.trim();
			if (reason && reason.length > 0) {
				throw new RegistryError(`Remote desktop engine plugin unavailable: ${reason}`, 409);
			}
			throw new RegistryError(
				`Remote desktop engine plugin status ${record.status.toLowerCase()}`,
				409
			);
		}

		const requiredVersion = version.trim();
		if (requiredVersion.length === 0) {
			return;
		}

		const reportedVersion = record.version?.trim() ?? '';
		if (!reportedVersion || reportedVersion !== requiredVersion) {
			const detail = reportedVersion ? ` (reported ${reportedVersion})` : '';
			throw new RegistryError(
				`Remote desktop engine plugin version ${requiredVersion} required${detail}`,
				409
			);
		}
	}

	sendRemoteDesktopInput(id: string, burst: RemoteDesktopInputBurst): boolean {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		const session = record.session;
		if (!session) {
			return false;
		}

		const socket = session.socket;
		if (!socket || (socket.readyState ?? 0) !== SOCKET_OPEN_STATE) {
			this.detachSession(record, session.id, { close: false });
			return false;
		}

		const envelope: AgentRemoteDesktopInputEnvelope = {
			type: 'remote-desktop-input',
			input: {
				sessionId: burst.sessionId,
				events: burst.events,
				sequence: burst.sequence
			}
		};

		try {
			socket.send(JSON.stringify(envelope));
			return true;
		} catch (err) {
			this.detachSession(record, session.id, { close: false });
			console.error('Failed to transmit remote desktop input burst', err);
			return false;
		}
	}

	sendAppVncInput(id: string, burst: AppVncInputBurst): boolean {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		const session = record.session;
		if (!session) {
			return false;
		}

		const socket = session.socket;
		if (!socket || (socket.readyState ?? 0) !== SOCKET_OPEN_STATE) {
			this.detachSession(record, session.id, { close: false });
			return false;
		}

		const envelope: AgentAppVncInputEnvelope = {
			type: 'app-vnc-input',
			input: {
				sessionId: burst.sessionId,
				events: burst.events,
				sequence: burst.sequence
			}
		};

		try {
			socket.send(JSON.stringify(envelope));
			return true;
		} catch (err) {
			this.detachSession(record, session.id, { close: false });
			console.error('Failed to transmit app VNC input burst', err);
			return false;
		}
	}

	disconnectAgent(id: string): AgentSnapshot {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		record.status = 'offline';
		record.lastSeen = new Date();
		const payload: AgentControlCommandPayload = { action: 'disconnect' };
		const command: Command = {
			id: randomUUID(),
			name: 'agent-control',
			payload,
			createdAt: new Date().toISOString()
		};
		command.signature = this.commandManager.signCommand(command);

		record.pendingCommands = [];
		let delivery: CommandDeliveryMode = 'session';
		if (!this.deliverViaSession(record, command)) {
			record.pendingCommands.push(command);
			this.clampPendingCommands(record, 'front');
			delivery = 'queued';
		}

		this.markDirty(record);
		this.notifyCommand(record, command, delivery);
		this.notifyAgentUpdate(record);
		return this.toSnapshot(record);
	}

	reconnectAgent(id: string): AgentSnapshot {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		const now = new Date();
		record.status = 'online';
		record.connectedAt = now;
		record.lastSeen = now;

		const payload: AgentControlCommandPayload = { action: 'reconnect' };
		const command: Command = {
			id: randomUUID(),
			name: 'agent-control',
			payload,
			createdAt: now.toISOString()
		};
		command.signature = this.commandManager.signCommand(command);

		let delivery: CommandDeliveryMode = 'session';
		if (!this.deliverViaSession(record, command)) {
			record.pendingCommands.unshift(command);
			this.clampPendingCommands(record, 'back');
			delivery = 'queued';
		}

		this.markDirty(record);
		this.notifyCommand(record, command, delivery);
		this.notifyAgentUpdate(record);
		return this.toSnapshot(record);
	}

	updateAgentTags(id: string, tags: string[]): AgentSnapshot {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		record.metadata = {
			...record.metadata,
			tags: utils.normalizeTags(Array.isArray(tags) ? tags : [])
		};

		this.markDirty(record);
		this.notifyAgentUpdate(record);
		return this.toSnapshot(record);
	}

	listAgents(): AgentSnapshot[] {
		return Array.from(this.agents.values()).map((record) => this.toSnapshot(record));
	}

	getAgent(id: string): AgentSnapshot {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}
		return this.toSnapshot(record);
	}

	getAgentOptionsState(id: string): OptionsState | null {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}
		return utils.cloneOptionsState(record.optionsState ?? null);
	}

	updateAgentOptionsState(id: string, state: OptionsState | null | undefined): OptionsState | null {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		record.optionsState = utils.cloneOptionsState(state ?? null);
		this.markDirty(record);
		return utils.cloneOptionsState(record.optionsState ?? null);
	}

	getDownloadsCatalogue(id: string): DownloadCatalogue {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}
		return utils.cloneDownloadCatalogue(record.downloadsCatalogue);
	}

	updateDownloadsCatalogue(
		id: string,
		entries: DownloadCatalogue | DownloadCatalogueEntry[] | null | undefined
	): DownloadCatalogue {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		const parsed = downloadCatalogueSchema.parse(entries ?? []);
		record.downloadsCatalogue = utils.cloneDownloadCatalogue(parsed);
		this.markDirty(record);
		return utils.cloneDownloadCatalogue(record.downloadsCatalogue);
	}

	authorizeAgent(id: string, key: string | undefined): void {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}
		if (!this.verifyAgentKey(record, key)) {
			throw new RegistryError('Invalid agent key', 401);
		}
		record.lastSeen = new Date();
	}

	peekCommands(id: string): Command[] {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}
		return [...record.pendingCommands];
	}

	getOperatorNote(id: string): AgentOperatorNote {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		if (!record.operatorNote) {
			return { note: '', tags: [], updatedAt: null, updatedBy: null } satisfies AgentOperatorNote;
		}

		return {
			note: record.operatorNote.note,
			tags: [...record.operatorNote.tags],
			updatedAt: record.operatorNote.updatedAt ? record.operatorNote.updatedAt.toISOString() : null,
			updatedBy: record.operatorNote.updatedBy
		} satisfies AgentOperatorNote;
	}

	updateOperatorNote(
		id: string,
		payload: { note?: string; tags?: string[] },
		options: { operatorId?: string } = {}
	): AgentOperatorNote {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		const normalizedNote = typeof payload.note === 'string' ? payload.note.trimEnd() : '';
		const normalizedTags = utils.normalizeTags(Array.isArray(payload.tags) ? payload.tags : []);
		const updatedAt = new Date();
		const updatedBy = options.operatorId ?? null;

		record.operatorNote = {
			note: normalizedNote,
			tags: normalizedTags,
			updatedAt,
			updatedBy
		} satisfies OperatorNoteRecord;

		this.markDirty(record);
		this.notifyAgentUpdate(record);

		return {
			note: normalizedNote,
			tags: [...normalizedTags],
			updatedAt: updatedAt.toISOString(),
			updatedBy
		} satisfies AgentOperatorNote;
	}

	syncSharedNotes(id: string, key: string | undefined, payload: NoteEnvelope[]): NoteEnvelope[] {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		if (!this.verifyAgentKey(record, key)) {
			throw new RegistryError('Invalid agent key', 401);
		}

		const now = new Date();
		let changed = false;
		for (const envelope of payload) {
			if (!envelope?.id) {
				continue;
			}
			const incomingUpdated = new Date(envelope.updatedAt ?? now.toISOString());
			const existing = record.sharedNotes.get(envelope.id);

			if (!existing) {
				record.sharedNotes.set(envelope.id, {
					id: envelope.id,
					ciphertext: envelope.ciphertext,
					nonce: envelope.nonce,
					digest: envelope.digest,
					version: envelope.version,
					updatedAt: incomingUpdated
				});
				changed = true;
				continue;
			}

			const shouldReplace =
				incomingUpdated.getTime() > existing.updatedAt.getTime() ||
				envelope.version > existing.version;

			if (shouldReplace) {
				existing.ciphertext = envelope.ciphertext;
				existing.nonce = envelope.nonce;
				existing.digest = envelope.digest;
				existing.version = envelope.version;
				existing.updatedAt = incomingUpdated;
				changed = true;
			}
		}

		if (changed) {
			this.markDirty(record);
			this.notifyNotes(record);
		}

		return this.serializeSharedNotes(record);
	}
}

export const registry = new AgentRegistry();
const shutdownHookKey = Symbol.for('tenvy.registry.shutdown');
type GlobalWithRegistryShutdownFlag = typeof globalThis & { [shutdownHookKey]?: boolean };
const globalWithRegistryShutdownFlag = globalThis as GlobalWithRegistryShutdownFlag;

const shutdownSignals = ['SIGINT', 'SIGTERM'] as const;
const shutdownSignalExitCodes: Record<(typeof shutdownSignals)[number], number> = {
	SIGINT: 130,
	SIGTERM: 143
};

let shutdownFlushPromise: Promise<void> | null = null;

async function flushRegistryBeforeExit(reason: string): Promise<void> {
	if (shutdownFlushPromise) {
		return shutdownFlushPromise;
	}

	shutdownFlushPromise = registry.flush().catch((error) => {
		console.error(`Failed to flush agent registry during ${reason}`, error);
	});

	return shutdownFlushPromise;
}

if (!globalWithRegistryShutdownFlag[shutdownHookKey]) {
	for (const signal of shutdownSignals) {
		process.once(signal, (received) => {
			const signalName = received as (typeof shutdownSignals)[number];
			const exitCode = shutdownSignalExitCodes[signalName] ?? 0;
			void flushRegistryBeforeExit(`signal ${received}`).finally(() => {
				process.exit(exitCode);
			});
		});
	}

	process.once('beforeExit', () => {
		void flushRegistryBeforeExit('beforeExit');
	});

	globalWithRegistryShutdownFlag[shutdownHookKey] = true;
}

export { RegistryError };
export type { AgentRegistryEvent } from '../../../../../shared/types/registry-events';
