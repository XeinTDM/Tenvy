import { createHash, createHmac, randomBytes, randomUUID, timingSafeEqual } from 'crypto';
import { and, eq, inArray } from 'drizzle-orm';
import { db } from '$lib/server/db';
import {
	agent as agentTable,
	agentNote as agentNoteTable,
	agentCommand as agentCommandTable,
	agentResult as agentResultTable,
	auditEvent as auditEventTable,
	registrySubscription as registrySubscriptionTable
} from '$lib/server/db/schema';
import {
	defaultAgentConfig,
	type AgentConfig,
	type AgentPluginConfig,
	type AgentPluginSignaturePolicy
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
	CommandAcknowledgementRecord,
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
	CommandOutputStreamRecord,
	OperatorNoteRecord,
	SharedNoteRecord,
	SessionTokenRecord
} from './types';
import { logger } from '../logger';

const MAX_TAGS = 16;
const MAX_TAG_LENGTH = 32;
const TAG_PATTERN = /^[\p{L}\p{N}_\-\s]+$/u;

const MAX_RECENT_RESULTS = 25;
export const MAX_PENDING_COMMANDS = 200;
const PENDING_COMMAND_DROP_WARN_INTERVAL_MS = 30_000;
const PERSIST_DEBOUNCE_MS = 2_000;
const SESSION_TOKEN_TTL_MS = 60_000;
const COMMAND_OUTPUT_RETENTION_MS = 5 * 60 * 1000;
const INACTIVITY_CHECK_INTERVAL_MS = 15_000;
const INACTIVITY_TIMEOUT_MULTIPLIER = 2;
const MIN_INACTIVITY_TIMEOUT_MS = 15_000;

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

interface CommandOutputSubscription {
	events: CommandOutputEvent[];
	completed: boolean;
	unsubscribe: () => void;
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

function ensureMetadata(metadata: AgentMetadata, remoteAddress?: string): AgentMetadata {
	if (!remoteAddress) {
		return metadata;
	}

	const next: AgentMetadata = { ...metadata };

	if (!next.ipAddress) {
		next.ipAddress = remoteAddress;
	}

	if (!next.publicIpAddress || next.publicIpAddress.trim() === '') {
		next.publicIpAddress = remoteAddress;
	}

	return next;
}

function validateToken(requestToken: string | undefined) {
	const expected = process.env.TENVY_SHARED_SECRET;
	if (expected && expected !== requestToken) {
		throw new RegistryError('Invalid registration token', 401);
	}
}

function computeFingerprint(metadata: AgentMetadata): string {
	const normalize = (value: string | undefined) => value?.trim().toLowerCase() ?? '';
	const hash = createHash('sha256');
	hash.update(normalize(metadata.hostname));
	hash.update('|');
	hash.update(normalize(metadata.username));
	hash.update('|');
	hash.update(normalize(metadata.os));
	hash.update('|');
	hash.update(normalize(metadata.architecture));
	hash.update('|');
	hash.update(normalize(metadata.group));
	hash.update('|');
	hash.update(normalize(metadata.hardwareId));
	return hash.digest('hex');
}

function hashAgentKey(rawKey: string): string {
	const hash = createHash('sha256');
	hash.update(rawKey, 'utf-8');
	return hash.digest('hex');
}

function hashSessionToken(rawToken: string): string {
	const hash = createHash('sha256');
	hash.update(rawToken, 'utf-8');
	return hash.digest('hex');
}

function hashCommandPayload(payload: Command['payload']): string {
	const hash = createHash('sha256');
	try {
		const serialized = JSON.stringify(payload ?? {});
		hash.update(serialized, 'utf-8');
	} catch {
		hash.update('unserializable', 'utf-8');
	}
	return hash.digest('hex');
}

function sanitizeAcknowledgement(
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

function deserializeAcknowledgement(value: string | null): CommandAcknowledgementRecord | null {
	if (!value) {
		return null;
	}

	try {
		const parsed = JSON.parse(value) as CommandAcknowledgementRecord;
		return sanitizeAcknowledgement(parsed);
	} catch {
		return null;
	}
}

function cloneOptionsFile(
	file: OptionsScriptFile | null | undefined
): OptionsScriptFile | null | undefined {
	if (file === null || file === undefined) {
		return file ?? undefined;
	}
	return { ...file } satisfies OptionsScriptFile;
}

function cloneOptionsConfig(
	config: OptionsScriptConfig | null | undefined
): OptionsScriptConfig | null | undefined {
	if (config === null || config === undefined) {
		return config ?? undefined;
	}
	const clone: OptionsScriptConfig = { ...config };
	if (config.file === null) {
		clone.file = null;
	} else if (config.file !== undefined) {
		clone.file = cloneOptionsFile(config.file) ?? undefined;
	}
	return clone;
}

function cloneOptionsRuntime(
	runtime: OptionsScriptRuntimeState | null | undefined
): OptionsScriptRuntimeState | null | undefined {
	if (runtime === null || runtime === undefined) {
		return runtime ?? undefined;
	}
	return { ...runtime } satisfies OptionsScriptRuntimeState;
}

function cloneOptionsState(state: OptionsState | null | undefined): OptionsState | null {
	if (state === null || state === undefined) {
		return state ?? null;
	}
	const clone: OptionsState = { ...state };
	if (state.script === null) {
		clone.script = null;
	} else if (state.script !== undefined) {
		clone.script = cloneOptionsConfig(state.script) ?? undefined;
	}
	if (state.scriptRuntime === null) {
		clone.scriptRuntime = null;
	} else if (state.scriptRuntime !== undefined) {
		clone.scriptRuntime = cloneOptionsRuntime(state.scriptRuntime) ?? undefined;
	}
	return clone;
}

function timingSafeEqualHex(expected: string, candidate: string): boolean {
	if (expected.length !== candidate.length) {
		return false;
	}

	try {
		const expectedBuffer = Buffer.from(expected, 'hex');
		const candidateBuffer = Buffer.from(candidate, 'hex');
		return timingSafeEqual(expectedBuffer, candidateBuffer);
	} catch {
		return false;
	}
}

function generateAgentKey(): { token: string; hash: string } {
	const token = randomBytes(32).toString('hex');
	return { token, hash: hashAgentKey(token) };
}

function generateSessionToken(): { token: string; hash: string; expiresAt: number } {
	const token = randomBytes(32).toString('hex');
	return { token, hash: hashSessionToken(token), expiresAt: Date.now() + SESSION_TOKEN_TTL_MS };
}

function parseNumeric(value: unknown): number | null {
	if (typeof value === 'number') {
		return Number.isFinite(value) ? value : null;
	}
	if (typeof value === 'string' && value.trim() !== '') {
		const parsed = Number(value);
		return Number.isFinite(parsed) ? parsed : null;
	}
	return null;
}

function cloneSignaturePolicy(
	policy: AgentPluginSignaturePolicy | undefined
): AgentPluginSignaturePolicy | undefined {
	if (!policy) {
		return undefined;
	}

	const cloned: AgentPluginSignaturePolicy = { ...policy };

	if (Array.isArray(policy.sha256AllowList)) {
		cloned.sha256AllowList = [...policy.sha256AllowList];
	}

	if (policy.ed25519PublicKeys) {
		cloned.ed25519PublicKeys = { ...policy.ed25519PublicKeys };
	}

	return cloned;
}

function clonePluginConfig(config?: AgentPluginConfig | null): AgentPluginConfig | undefined {
	if (!config || typeof config !== 'object') {
		return undefined;
	}

	const clone: AgentPluginConfig = {};

	for (const [key, value] of Object.entries(config)) {
		if (key === 'signaturePolicy') {
			continue;
		}
		(clone as Record<string, unknown>)[key] = value;
	}

	return clone;
}

function normalizeConfig(config?: Partial<AgentConfig> | null): AgentConfig {
	const normalized: AgentConfig = {
		...defaultAgentConfig
	};

	if (!config) {
		return normalized;
	}

	const pollInterval = parseNumeric(config.pollIntervalMs);
	if (pollInterval !== null && pollInterval > 0) {
		normalized.pollIntervalMs = Math.max(1, Math.round(pollInterval));
	}

	const maxBackoff = parseNumeric(config.maxBackoffMs);
	if (maxBackoff !== null && maxBackoff > 0) {
		normalized.maxBackoffMs = Math.max(normalized.pollIntervalMs, Math.round(maxBackoff));
	}

	const jitter = parseNumeric(config.jitterRatio);
	if (jitter !== null && jitter >= 0 && jitter <= 1) {
		normalized.jitterRatio = jitter;
	}

	const pluginConfig = clonePluginConfig(config?.plugins);
	const signaturePolicy = cloneSignaturePolicy(getAgentSignaturePolicy());

	const mergedPluginConfig: AgentPluginConfig = {
		...(pluginConfig ?? {})
	};

	if (signaturePolicy) {
		mergedPluginConfig.signaturePolicy = signaturePolicy;
	}

	if (Object.keys(mergedPluginConfig).length > 0) {
		normalized.plugins = mergedPluginConfig;
	}

	return normalized;
}

function cloneMetadata(metadata: AgentMetadata): AgentMetadata {
	const clone: AgentMetadata = { ...metadata };
	if (Array.isArray(metadata.tags)) {
		clone.tags = [...metadata.tags];
	}
	if (metadata.location) {
		clone.location = { ...metadata.location };
	}
	return clone;
}

function cloneMetrics(metrics: AgentMetrics | undefined): AgentMetrics | undefined {
	return metrics ? { ...metrics } : undefined;
}

function cloneDownloadEntry(entry: DownloadCatalogueEntry): DownloadCatalogueEntry {
	const clone: DownloadCatalogueEntry = { ...entry };
	if (Array.isArray(entry.tags)) {
		clone.tags = [...entry.tags];
	}
	return clone;
}

function cloneDownloadCatalogue(
	entries: DownloadCatalogue | DownloadCatalogueEntry[] | null | undefined
): DownloadCatalogue {
	if (!entries || entries.length === 0) {
		return [];
	}
	return entries.map((entry) => cloneDownloadEntry(entry));
}

function cloneCommandOutputEvent(event: CommandOutputEvent): CommandOutputEvent {
	if (event.type === 'chunk') {
		return { ...event };
	}
	return {
		type: 'end',
		commandId: event.commandId,
		timestamp: event.timestamp,
		result: { ...event.result }
	} satisfies CommandOutputEvent;
}

function normalizeCommandOutputEvent(
	commandId: string,
	event: CommandOutputEvent
): CommandOutputEvent {
	const timestamp =
		typeof event.timestamp === 'string' && event.timestamp.trim() !== ''
			? event.timestamp
			: new Date().toISOString();

	if (event.type === 'chunk') {
		const sequence = Number.isFinite(event.sequence) ? Number(event.sequence) : 0;
		return {
			type: 'chunk',
			commandId,
			sequence,
			data: typeof event.data === 'string' ? event.data : '',
			timestamp
		} satisfies CommandOutputEvent;
	}

	const baseResult = event.result ?? {
		commandId,
		success: false,
		output: undefined,
		error: 'Command result unavailable',
		completedAt: timestamp
	};

	const completedAt =
		typeof baseResult.completedAt === 'string' && baseResult.completedAt.trim() !== ''
			? baseResult.completedAt
			: timestamp;

	const normalizedCommandId =
		typeof baseResult.commandId === 'string' && baseResult.commandId.trim() !== ''
			? baseResult.commandId
			: commandId;

	return {
		type: 'end',
		commandId,
		timestamp,
		result: {
			commandId: normalizedCommandId,
			success: Boolean(baseResult.success),
			output: baseResult.output ?? undefined,
			error: baseResult.error ?? undefined,
			completedAt
		}
	} satisfies CommandOutputEvent;
}

function parseCompletedAt(result: CommandResult | undefined): number {
	if (!result) {
		return 0;
	}
	if (typeof result.completedAt === 'string') {
		const parsed = Date.parse(result.completedAt);
		if (Number.isFinite(parsed)) {
			return parsed;
		}
	}
	return 0;
}

function mergeRecentResults(existing: CommandResult[], incoming: CommandResult[]): CommandResult[] {
	if (existing.length === 0 && incoming.length === 0) {
		return [];
	}

	const merged = new Map<string, { result: CommandResult; timestamp: number }>();

	const upsert = (candidate: CommandResult | null | undefined) => {
		if (!candidate?.commandId) {
			return;
		}
		const timestamp = parseCompletedAt(candidate);
		const current = merged.get(candidate.commandId);
		if (!current || timestamp >= current.timestamp) {
			merged.set(candidate.commandId, {
				result: { ...candidate },
				timestamp
			});
		}
	};

	for (const result of existing) {
		upsert(result);
	}

	for (const result of incoming) {
		upsert(result);
	}

	return Array.from(merged.values())
		.sort((a, b) => {
			if (b.timestamp !== a.timestamp) {
				return b.timestamp - a.timestamp;
			}
			return b.result.commandId.localeCompare(a.result.commandId);
		})
		.slice(0, MAX_RECENT_RESULTS)
		.map((entry) => entry.result);
}

export class AgentRegistry {
	private readonly agents = new Map<string, AgentRecord>();
	private readonly fingerprints = new Map<string, string>();
	private readonly sessionTokens = new Map<string, SessionTokenRecord>();
	private readonly subscribers = new Map<string, AgentRegistrySubscriber>();
	private readonly adminSubscriptions = new Map<string, AdminSubscriptionRecord>();
	private readonly commandOutputStreams = new Map<string, Map<string, CommandOutputStreamRecord>>();
	private persistTimer: ReturnType<typeof setTimeout> | null = null;
	private persistPromise: Promise<void> | null = null;
	private needsPersist = false;
	private broadcastSequence = 0;
	private readonly pluginTelemetry: PluginTelemetryStore;
	private readonly subscriptionStore = new RegistrySubscriptionStore();
	private inactivityCheckTimer: ReturnType<typeof setInterval> | null = null;

	constructor() {
		this.loadFromDatabase();
		this.pluginTelemetry = new PluginTelemetryStore();
		this.startInactivityMonitor();
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
					this.schedulePersist();
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

		for (const record of this.adminSubscriptions.values()) {
			try {
				record.listener(event);
			} catch (error) {
				console.error('Agent registry subscriber failed', error);
			}

			record.cursor = sequence;
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

	private getCommandOutputStream(
		agentId: string,
		commandId: string,
		create: boolean
	): CommandOutputStreamRecord | null {
		let streams = this.commandOutputStreams.get(agentId);
		if (!streams) {
			if (!create) {
				return null;
			}
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
			if (!streams) {
				return;
			}
			const target = streams.get(commandId);
			if (!target) {
				return;
			}
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

	private notifyCommand(
		record: AgentRecord,
		command: Command,
		delivery: CommandDeliveryMode
	): void {
		this.broadcast({
			type: 'command',
			agentId: record.id,
			delivery,
			command: { ...command }
		});
	}

	private logCommandQueued(
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

	private logCommandExecuted(agentId: string, result: CommandResult): void {
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

	private verifyAgentKey(record: AgentRecord, key: string | undefined): boolean {
		if (!key) {
			return false;
		}

		const incomingHash = hashAgentKey(key);
		return timingSafeEqualHex(record.keyHash, incomingHash);
	}

	private consumeSessionToken(record: AgentRecord, token: string | undefined): void {
		if (!token) {
			throw new RegistryError('Missing session token', 401);
		}

		const stored = this.sessionTokens.get(record.id);
		if (!stored) {
			throw new RegistryError('Invalid session token', 401);
		}

		if (Date.now() >= stored.expiresAt) {
			this.sessionTokens.delete(record.id);
			throw new RegistryError('Session token expired', 401);
		}

		const incomingHash = hashSessionToken(token);
		if (!timingSafeEqualHex(stored.hash, incomingHash)) {
			this.sessionTokens.delete(record.id);
			throw new RegistryError('Invalid session token', 401);
		}

		this.sessionTokens.delete(record.id);
	}

	private getAgentRecord(id: string): AgentRecord | undefined {
		const record = this.agents.get(id);
		if (record) {
			return record;
		}

		// Try to load from database
		const loaded = this.loadAgentFromDatabase(id);
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

		// Try to load from database by fingerprint
		const loaded = this.loadAgentFromDatabaseByFingerprint(fingerprint);
		if (loaded) {
			this.agents.set(loaded.id, loaded);
			this.fingerprints.set(loaded.fingerprint, loaded.id);
			return loaded;
		}

		return undefined;
	}

	private loadAgentFromDatabase(id: string): AgentRecord | null {
		const row = db.select().from(agentTable).where(eq(agentTable.id, id)).get();
		if (!row) return null;
		return this.rowToRecord(row);
	}

	private loadAgentFromDatabaseByFingerprint(fingerprint: string): AgentRecord | null {
		const row = db.select().from(agentTable).where(eq(agentTable.fingerprint, fingerprint)).get();
		if (!row) return null;
		return this.rowToRecord(row);
	}

	private rowToRecord(row: typeof agentTable.$inferSelect): AgentRecord {
		let metadata: AgentMetadata | null = null;
		let config: AgentConfig | null = null;
		let metrics: AgentMetrics | undefined;
		let optionsState: OptionsState | null = null;
		let downloadsCatalogue: DownloadCatalogue = [];

		try {
			metadata = JSON.parse(row.metadata) as AgentMetadata;
		} catch {
			metadata = null;
		}

		if (!metadata) {
			throw new Error(`Invalid metadata for agent ${row.id}`);
		}

		try {
			config = JSON.parse(row.config) as AgentConfig;
		} catch {
			config = null;
		}

		if (row.metrics) {
			try {
				metrics = JSON.parse(row.metrics) as AgentMetrics;
			} catch {
				metrics = undefined;
			}
		}

		if (row.optionsState) {
			try {
				const parsed = JSON.parse(row.optionsState) as OptionsState;
				optionsState = cloneOptionsState(parsed);
			} catch {
				optionsState = null;
			}
		}

		if (row.downloadsCatalogue) {
			try {
				const parsed = JSON.parse(row.downloadsCatalogue) as unknown;
				downloadsCatalogue = downloadCatalogueSchema.parse(parsed);
			} catch {
				downloadsCatalogue = [];
			}
		}

		const normalizedMetadata: AgentMetadata = {
			...metadata,
			tags: Array.isArray(metadata.tags)
				? this.normalizeTags(metadata.tags.filter((tag): tag is string => typeof tag === 'string'))
				: metadata.tags
		};

		const connectedAt =
			row.connectedAt instanceof Date ? row.connectedAt : new Date(row.connectedAt ?? Date.now());
		const lastSeen =
			row.lastSeen instanceof Date ? row.lastSeen : new Date(row.lastSeen ?? connectedAt);

		// Load sub-tables
		const noteRows = db
			.select()
			.from(agentNoteTable)
			.where(eq(agentNoteTable.agentId, row.id))
			.all();
		const sharedNotes = new Map<string, SharedNoteRecord>();
		for (const nr of noteRows) {
			sharedNotes.set(nr.noteId, {
				id: nr.noteId,
				ciphertext: nr.ciphertext,
				nonce: nr.nonce,
				digest: nr.digest,
				version: nr.version ?? 1,
				updatedAt:
					nr.updatedAt instanceof Date ? nr.updatedAt : new Date(nr.updatedAt ?? Date.now())
			});
		}

		const commandRows = db
			.select()
			.from(agentCommandTable)
			.where(eq(agentCommandTable.agentId, row.id))
			.orderBy(agentCommandTable.createdAt)
			.all();
		const pendingCommands: Command[] = commandRows.map((cr) => {
			let payload: Command['payload'];
			try {
				payload = cr.payload
					? (JSON.parse(cr.payload) as Command['payload'])
					: ({} as Command['payload']);
			} catch {
				payload = {} as Command['payload'];
			}
			return {
				id: cr.id,
				name: cr.name as Command['name'],
				payload,
				createdAt: (cr.createdAt instanceof Date
					? cr.createdAt
					: new Date(cr.createdAt ?? Date.now())
				).toISOString()
			};
		});

		const resultRows = db
			.select()
			.from(agentResultTable)
			.where(eq(agentResultTable.agentId, row.id))
			.orderBy(agentResultTable.completedAt)
			.all();
		const recentResults = mergeRecentResults(
			[],
			resultRows.map((rr) => ({
				commandId: rr.commandId,
				success: Boolean(rr.success),
				output: rr.output ?? undefined,
				error: rr.error ?? undefined,
				completedAt: (rr.completedAt instanceof Date
					? rr.completedAt
					: new Date(rr.completedAt ?? Date.now())
				).toISOString()
			}))
		);

		let operatorNote: OperatorNoteRecord | null = null;
		if (
			row.operatorNote !== null ||
			row.operatorNoteTags !== null ||
			row.operatorNoteUpdatedAt !== null ||
			row.operatorNoteUpdatedBy !== null
		) {
			let parsedTags: string[] = [];
			if (typeof row.operatorNoteTags === 'string') {
				try {
					const parsed = JSON.parse(row.operatorNoteTags) as unknown;
					if (Array.isArray(parsed)) {
						parsedTags = parsed
							.map((tag) => (typeof tag === 'string' ? tag : `${tag}`))
							.map((tag) => tag.trim())
							.filter((tag) => tag.length > 0);
					}
				} catch {
					parsedTags = [];
				}
			}

			operatorNote = {
				note: row.operatorNote ?? '',
				tags: parsedTags,
				updatedAt:
					row.operatorNoteUpdatedAt instanceof Date
						? row.operatorNoteUpdatedAt
						: row.operatorNoteUpdatedAt
							? new Date(row.operatorNoteUpdatedAt)
							: null,
				updatedBy: row.operatorNoteUpdatedBy ?? null
			};
		}

		return {
			id: row.id,
			keyHash: row.keyHash,
			metadata: normalizedMetadata,
			status: row.status as AgentStatus,
			connectedAt,
			lastSeen,
			metrics,
			config: normalizeConfig(config),
			pendingCommands,
			recentResults,
			sharedNotes,
			operatorNote,
			fingerprint: row.fingerprint,
			optionsState: optionsState ? cloneOptionsState(optionsState) : null,
			downloadsCatalogue: cloneDownloadCatalogue(downloadsCatalogue)
		};
	}

	private loadFromDatabase(): void {
		let agentRows: Array<typeof agentTable.$inferSelect> = [];
		try {
			agentRows = db.select().from(agentTable).all();
		} catch (error) {
			console.error('Failed to read agent registry from database', error);
			return;
		}

		let noteRows: Array<typeof agentNoteTable.$inferSelect> = [];
		let commandRows: Array<typeof agentCommandTable.$inferSelect> = [];
		let resultRows: Array<typeof agentResultTable.$inferSelect> = [];

		try {
			noteRows = db.select().from(agentNoteTable).all();
		} catch (error) {
			console.error('Failed to read agent notes from database', error);
		}

		try {
			commandRows = db.select().from(agentCommandTable).orderBy(agentCommandTable.createdAt).all();
		} catch (error) {
			console.error('Failed to read agent commands from database', error);
		}

		try {
			resultRows = db.select().from(agentResultTable).orderBy(agentResultTable.completedAt).all();
		} catch (error) {
			console.error('Failed to read agent results from database', error);
		}

		this.agents.clear();
		this.fingerprints.clear();

		const notesByAgent = new Map<string, Map<string, SharedNoteRecord>>();
		for (const row of noteRows) {
			const updatedAt =
				row.updatedAt instanceof Date ? row.updatedAt : new Date(row.updatedAt ?? Date.now());
			if (!notesByAgent.has(row.agentId)) {
				notesByAgent.set(row.agentId, new Map());
			}
			notesByAgent.get(row.agentId)!.set(row.noteId, {
				id: row.noteId,
				ciphertext: row.ciphertext,
				nonce: row.nonce,
				digest: row.digest,
				version: row.version ?? 1,
				updatedAt
			});
		}

		const commandsByAgent = new Map<string, Command[]>();
		for (const row of commandRows) {
			let payload: Command['payload'];
			try {
				payload = row.payload
					? (JSON.parse(row.payload) as Command['payload'])
					: ({} as Command['payload']);
			} catch {
				payload = {} as Command['payload'];
			}
			if (!commandsByAgent.has(row.agentId)) {
				commandsByAgent.set(row.agentId, []);
			}
			commandsByAgent.get(row.agentId)!.push({
				id: row.id,
				name: row.name as Command['name'],
				payload,
				createdAt: (row.createdAt instanceof Date
					? row.createdAt
					: new Date(row.createdAt ?? Date.now())
				).toISOString()
			});
		}

		const resultsByAgent = new Map<string, CommandResult[]>();
		for (const row of resultRows) {
			if (!resultsByAgent.has(row.agentId)) {
				resultsByAgent.set(row.agentId, []);
			}
			resultsByAgent.get(row.agentId)!.push({
				commandId: row.commandId,
				success: Boolean(row.success),
				output: row.output ?? undefined,
				error: row.error ?? undefined,
				completedAt: (row.completedAt instanceof Date
					? row.completedAt
					: new Date(row.completedAt ?? Date.now())
				).toISOString()
			});
		}

		for (const row of agentRows) {
			let metadata: AgentMetadata | null = null;
			let config: AgentConfig | null = null;
			let metrics: AgentMetrics | undefined;
			let optionsState: OptionsState | null = null;
			let downloadsCatalogue: DownloadCatalogue = [];

			try {
				metadata = JSON.parse(row.metadata) as AgentMetadata;
			} catch {
				metadata = null;
			}

			if (!metadata) {
				continue;
			}

			try {
				config = JSON.parse(row.config) as AgentConfig;
			} catch {
				config = null;
			}

			if (row.metrics) {
				try {
					metrics = JSON.parse(row.metrics) as AgentMetrics;
				} catch {
					metrics = undefined;
				}
			}

			if (row.optionsState) {
				try {
					const parsed = JSON.parse(row.optionsState) as OptionsState;
					optionsState = cloneOptionsState(parsed);
				} catch {
					optionsState = null;
				}
			}

			if (row.downloadsCatalogue) {
				try {
					const parsed = JSON.parse(row.downloadsCatalogue) as unknown;
					downloadsCatalogue = downloadCatalogueSchema.parse(parsed);
				} catch {
					downloadsCatalogue = [];
				}
			}

			const normalizedMetadata: AgentMetadata = {
				...metadata,
				tags: Array.isArray(metadata.tags)
					? this.normalizeTags(
							metadata.tags.filter((tag): tag is string => typeof tag === 'string')
						)
					: metadata.tags
			};

			const connectedAt =
				row.connectedAt instanceof Date ? row.connectedAt : new Date(row.connectedAt ?? Date.now());
			const lastSeen =
				row.lastSeen instanceof Date ? row.lastSeen : new Date(row.lastSeen ?? connectedAt);
			const sharedNotes = notesByAgent.get(row.id) ?? new Map<string, SharedNoteRecord>();
			const pendingCommands = commandsByAgent.get(row.id) ?? [];
			const recentResults = mergeRecentResults([], resultsByAgent.get(row.id) ?? []);
			let operatorNote: OperatorNoteRecord | null = null;

			if (
				row.operatorNote !== null ||
				row.operatorNoteTags !== null ||
				row.operatorNoteUpdatedAt !== null ||
				row.operatorNoteUpdatedBy !== null
			) {
				let parsedTags: string[] = [];
				if (typeof row.operatorNoteTags === 'string') {
					try {
						const parsed = JSON.parse(row.operatorNoteTags) as unknown;
						if (Array.isArray(parsed)) {
							parsedTags = parsed
								.map((tag) => (typeof tag === 'string' ? tag : `${tag}`))
								.map((tag) => tag.trim())
								.filter((tag) => tag.length > 0);
						}
					} catch {
						parsedTags = [];
					}
				}

				operatorNote = {
					note: row.operatorNote ?? '',
					tags: parsedTags,
					updatedAt:
						row.operatorNoteUpdatedAt instanceof Date
							? row.operatorNoteUpdatedAt
							: row.operatorNoteUpdatedAt
								? new Date(row.operatorNoteUpdatedAt)
								: null,
					updatedBy: row.operatorNoteUpdatedBy ?? null
				};
			}

			const record: AgentRecord = {
				id: row.id,
				keyHash: row.keyHash,
				metadata: normalizedMetadata,
				status: row.status as AgentStatus,
				connectedAt,
				lastSeen,
				metrics,
				config: normalizeConfig(config),
				pendingCommands,
				recentResults,
				sharedNotes,
				operatorNote,
				fingerprint: row.fingerprint,
				optionsState: optionsState ? cloneOptionsState(optionsState) : null,
				downloadsCatalogue: cloneDownloadCatalogue(downloadsCatalogue)
			};

			this.agents.set(record.id, record);
			this.fingerprints.set(record.fingerprint, record.id);
		}
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
		const agents = Array.from(this.agents.values());
		const now = new Date();
		const agentIds = agents.map((agent) => agent.id);

		await db.transaction((tx) => {
			if (agentIds.length === 0) {
				tx.delete(agentNoteTable).run();
				tx.delete(agentCommandTable).run();
				tx.delete(agentResultTable).run();
				tx.delete(agentTable).run();
				return;
			}

			const existing = tx
				.select({ id: agentTable.id })
				.from(agentTable)
				.where(inArray(agentTable.id, agentIds))
				.all();
			const existingIds = new Set(existing.map((row) => row.id));

			for (const record of agents) {
				const payload = {
					id: record.id,
					keyHash: record.keyHash,
					metadata: JSON.stringify(record.metadata),
					status: record.status,
					connectedAt: record.connectedAt,
					lastSeen: record.lastSeen,
					metrics: record.metrics ? JSON.stringify(record.metrics) : null,
					config: JSON.stringify(record.config),
					optionsState: record.optionsState ? JSON.stringify(record.optionsState) : null,
					downloadsCatalogue:
						record.downloadsCatalogue.length > 0 ? JSON.stringify(record.downloadsCatalogue) : null,
					operatorNote: record.operatorNote ? record.operatorNote.note : null,
					operatorNoteTags: record.operatorNote ? JSON.stringify(record.operatorNote.tags) : null,
					operatorNoteUpdatedAt: record.operatorNote?.updatedAt ?? null,
					operatorNoteUpdatedBy: record.operatorNote?.updatedBy ?? null,
					fingerprint: record.fingerprint,
					createdAt: record.connectedAt,
					updatedAt: now
				};

				if (existingIds.has(record.id)) {
					tx.update(agentTable)
						.set({
							keyHash: payload.keyHash,
							metadata: payload.metadata,
							status: payload.status,
							connectedAt: payload.connectedAt,
							lastSeen: payload.lastSeen,
							metrics: payload.metrics,
							config: payload.config,
							downloadsCatalogue: payload.downloadsCatalogue,
							optionsState: payload.optionsState,
							operatorNote: payload.operatorNote,
							operatorNoteTags: payload.operatorNoteTags,
							operatorNoteUpdatedAt: payload.operatorNoteUpdatedAt,
							operatorNoteUpdatedBy: payload.operatorNoteUpdatedBy,
							fingerprint: payload.fingerprint,
							updatedAt: payload.updatedAt
						})
						.where(eq(agentTable.id, record.id))
						.run();
				} else {
					tx.insert(agentTable).values(payload).run();
					existingIds.add(record.id);
				}

				tx.delete(agentNoteTable).where(eq(agentNoteTable.agentId, record.id)).run();
				const notes = Array.from(record.sharedNotes.values());
				if (notes.length > 0) {
					tx.insert(agentNoteTable)
						.values(
							notes.map((note) => ({
								agentId: record.id,
								noteId: note.id,
								ciphertext: note.ciphertext,
								nonce: note.nonce,
								digest: note.digest,
								version: note.version,
								updatedAt: note.updatedAt
							}))
						)
						.run();
				}

				tx.delete(agentCommandTable).where(eq(agentCommandTable.agentId, record.id)).run();
				if (record.pendingCommands.length > 0) {
					tx.insert(agentCommandTable)
						.values(
							record.pendingCommands.map((command) => ({
								id: command.id,
								agentId: record.id,
								name: command.name,
								payload: JSON.stringify(command.payload ?? {}),
								createdAt: new Date(command.createdAt)
							}))
						)
						.run();
				}

				tx.delete(agentResultTable).where(eq(agentResultTable.agentId, record.id)).run();
				if (record.recentResults.length > 0) {
					tx.insert(agentResultTable)
						.values(
							record.recentResults.map((result) => ({
								agentId: record.id,
								commandId: result.commandId,
								success: result.success,
								output: result.output,
								error: result.error,
								completedAt: new Date(result.completedAt)
							}))
						)
						.run();
				}
			}
		});
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
			metadata: cloneMetadata(record.metadata),
			status: record.status,
			connectedAt: record.connectedAt.toISOString(),
			lastSeen: record.lastSeen.toISOString(),
			metrics: cloneMetrics(record.metrics),
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
		const session = record.session;
		if (!session || session.id !== sessionId) {
			return;
		}

		record.session = undefined;

		if (options.markOffline !== false) {
			record.status = 'offline';
			record.lastSeen = new Date();
			this.schedulePersist();
			this.notifyAgentUpdate(record);
		}

		if (options.close === false) {
			return;
		}

		try {
			session.socket.close(options.code ?? 1000, options.reason);
		} catch {
			// Ignore close failures.
		}
	}

	private deliverViaSession(record: AgentRecord, command: Command): boolean {
		const session = record.session;
		if (!session) {
			return false;
		}

		const socket = session.socket;
		if (!socket || (socket.readyState ?? 0) !== SOCKET_OPEN_STATE) {
			this.detachSession(record, session.id, { close: false });
			return false;
		}

		try {
			const envelope: AgentCommandEnvelope = { type: 'command', command };
			socket.send(JSON.stringify(envelope));
			return true;
		} catch {
			this.detachSession(record, session.id, { close: false });
			return false;
		}
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

	registerAgent(
		payload: AgentRegistrationRequest,
		options: { remoteAddress?: string } = {}
	): AgentRegistrationResponse {
		validateToken(payload.token);
		const now = new Date();
		const normalizedTags = this.normalizeTags(payload.metadata.tags ?? []);
		const incomingMetadata = ensureMetadata(
			{ ...payload.metadata, tags: normalizedTags.length > 0 ? normalizedTags : undefined },
			options.remoteAddress
		);
		const fingerprint = computeFingerprint(incomingMetadata);

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
			const nextMetadata: AgentMetadata = ensureMetadata(
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
			const nextKey = generateAgentKey();
			existingRecord.keyHash = nextKey.hash;
			existingRecord.config = normalizeConfig(existingRecord.config);
			existingRecord.fingerprint = computeFingerprint(nextMetadata);
			this.sessionTokens.delete(existingRecord.id);

			if (previousFingerprint !== existingRecord.fingerprint) {
				this.fingerprints.delete(previousFingerprint);
			}
			this.fingerprints.set(existingRecord.fingerprint, existingRecord.id);
			this.agents.set(existingRecord.id, existingRecord);
			this.schedulePersist();
			this.notifyAgentUpdate(existingRecord);

			return {
				agentId: existingRecord.id,
				agentKey: nextKey.token,
				config: { ...existingRecord.config },
				commands: [],
				serverTime: now.toISOString()
			};
		}

		this.fingerprints.delete(fingerprint);

		const id = randomUUID();
		const nextKey = generateAgentKey();
		const record: AgentRecord = {
			id,
			keyHash: nextKey.hash,
			metadata: incomingMetadata,
			status: 'online',
			connectedAt: now,
			lastSeen: now,
			metrics: undefined,
			config: normalizeConfig(null),
			pendingCommands: [],
			recentResults: [],
			sharedNotes: new Map(),
			operatorNote: null,
			fingerprint,
			optionsState: null,
			downloadsCatalogue: []
		};

		this.agents.set(id, record);
		this.fingerprints.set(fingerprint, id);
		this.sessionTokens.delete(id);
		this.schedulePersist();
		this.notifyAgentUpdate(record);

		return {
			agentId: id,
			agentKey: nextKey.token,
			config: { ...record.config },
			commands: [],
			serverTime: now.toISOString()
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

		const generated = generateSessionToken();
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
			record.metadata = ensureMetadata(record.metadata, options.remoteAddress);
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

		this.schedulePersist();
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
			record.metadata = ensureMetadata(record.metadata, options.remoteAddress);
		}
		if (payload.metrics) {
			record.metrics = { ...payload.metrics };
		}
		if (payload.results && payload.results.length > 0) {
			record.recentResults = mergeRecentResults(record.recentResults, payload.results);
			for (const result of payload.results) {
				this.logCommandExecuted(record.id, result);
			}
		}

		if (payload.options !== undefined) {
			record.optionsState = cloneOptionsState(payload.options);
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

		this.schedulePersist();

		const optionsPayload = cloneOptionsState(record.optionsState ?? null);

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
			record.metadata = ensureMetadata(record.metadata, options.remoteAddress);
		}

		const stream = this.getCommandOutputStream(id, commandId, true);
		if (!stream) {
			throw new RegistryError('Failed to create command output stream', 500);
		}

		const normalized = normalizeCommandOutputEvent(commandId, event);
		stream.events.push(normalized);

		for (const listener of stream.listeners) {
			try {
				listener(cloneCommandOutputEvent(normalized));
			} catch (err) {
				console.error('Command output listener failed', err);
			}
		}

		if (normalized.type === 'end') {
			stream.completed = true;
		}

		this.scheduleCommandOutputCleanup(id, commandId, stream);

		this.schedulePersist();
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

		const stream = this.getCommandOutputStream(id, commandId, true);
		if (!stream) {
			throw new RegistryError('Failed to create command output stream', 500);
		}

		this.clearCommandOutputCleanup(stream);
		stream.listeners.add(listener);
		this.scheduleCommandOutputCleanup(id, commandId, stream);

		const unsubscribe = () => {
			stream.listeners.delete(listener);
			if (stream.completed && stream.listeners.size === 0) {
				this.scheduleCommandOutputCleanup(id, commandId, stream);
			}
		};

		return {
			events: stream.events.map(cloneCommandOutputEvent),
			completed: stream.completed,
			unsubscribe
		} satisfies CommandOutputSubscription;
	}

	private signCommand(command: Command): string | undefined {
		const secret = process.env.TENVY_COMMAND_SECRET;
		if (!secret) {
			return undefined;
		}

		try {
			const hmac = createHmac('sha256', secret);
			const payloadString = command.payload ? JSON.stringify(command.payload) : '';
			const data = [command.id, command.name, payloadString, command.createdAt].join('|');
			hmac.update(data);
			return hmac.digest('hex');
		} catch (error) {
			console.error('Failed to sign command', error);
			return undefined;
		}
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

		command.signature = this.signCommand(command);

		const audit = this.logCommandQueued(
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

		this.schedulePersist();

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
		command.signature = this.signCommand(command);

		record.pendingCommands = [];
		let delivery: CommandDeliveryMode = 'session';
		if (!this.deliverViaSession(record, command)) {
			record.pendingCommands.push(command);
			this.clampPendingCommands(record, 'front');
			delivery = 'queued';
		}

		this.schedulePersist();
		this.notifyCommand(record, command, delivery);
		this.notifyAgentUpdate(record);
		return this.toSnapshot(record);
	}

	private normalizeTags(tags: string[]): string[] {
		const seen = new Set<string>();
		const result: string[] = [];

		for (const entry of tags) {
			if (typeof entry !== 'string') {
				continue;
			}

			const trimmed = entry.trim();
			if (trimmed.length === 0 || trimmed.length > MAX_TAG_LENGTH) {
				continue;
			}

			if (!TAG_PATTERN.test(trimmed)) {
				continue;
			}

			const key = trimmed.toLowerCase();
			if (seen.has(key)) {
				continue;
			}

			seen.add(key);
			result.push(trimmed);

			if (result.length >= MAX_TAGS) {
				break;
			}
		}

		return result;
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
		command.signature = this.signCommand(command);

		let delivery: CommandDeliveryMode = 'session';
		if (!this.deliverViaSession(record, command)) {
			record.pendingCommands.unshift(command);
			this.clampPendingCommands(record, 'back');
			delivery = 'queued';
		}

		this.schedulePersist();
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
			tags: this.normalizeTags(Array.isArray(tags) ? tags : [])
		};

		this.schedulePersist();
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
		return cloneOptionsState(record.optionsState ?? null);
	}

	updateAgentOptionsState(id: string, state: OptionsState | null | undefined): OptionsState | null {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}

		record.optionsState = cloneOptionsState(state ?? null);
		this.schedulePersist();
		return cloneOptionsState(record.optionsState ?? null);
	}

	getDownloadsCatalogue(id: string): DownloadCatalogue {
		const record = this.getAgentRecord(id);
		if (!record) {
			throw new RegistryError('Agent not found', 404);
		}
		return cloneDownloadCatalogue(record.downloadsCatalogue);
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
		record.downloadsCatalogue = cloneDownloadCatalogue(parsed);
		this.schedulePersist();
		return cloneDownloadCatalogue(record.downloadsCatalogue);
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
		const normalizedTags = this.normalizeTags(Array.isArray(payload.tags) ? payload.tags : []);
		const updatedAt = new Date();
		const updatedBy = options.operatorId ?? null;

		record.operatorNote = {
			note: normalizedNote,
			tags: normalizedTags,
			updatedAt,
			updatedBy
		} satisfies OperatorNoteRecord;

		this.schedulePersist();
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
			this.schedulePersist();
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
