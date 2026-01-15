import { randomUUID } from 'node:crypto';
import {
	type AppVncApplicationDescriptor,
	type AppVncCommandPayload,
	type AppVncCursorState,
	type AppVncFramePacket,
	type AppVncInputEvent,
	type AppVncInputBurst,
	type AppVncSessionMetadata,
	type AppVncSessionSettings,
	type AppVncSessionSettingsPatch,
	type AppVncSessionState,
	type AppVncVirtualizationHints,
	type AppVncVirtualizationPlan
} from '$lib/types/app-vnc';
import { findAppVncApplication } from '$lib/data/app-vnc-apps';
import { resolveSeedPlan } from './app-vnc-seeds';
import { registry, RegistryError } from './store';

const encoder = new TextEncoder();
const HEARTBEAT_INTERVAL_MS = 15_000;
const HISTORY_LIMIT = 24;
const MAX_FRAME_WIDTH = 4_096;
const MAX_FRAME_HEIGHT = 4_096;
const MAX_BASE64_SIZE = 8 * 1024 * 1024;

const defaultSettings: AppVncSessionSettings = Object.freeze({
	monitor: 'Primary',
	quality: 'balanced',
	captureCursor: true,
	clipboardSync: false,
	blockLocalInput: false,
	heartbeatInterval: 30
});

const qualities = new Set<AppVncSessionSettings['quality']>(['lossless', 'balanced', 'bandwidth']);

export class AppVncError extends Error {
	status: number;

	constructor(message: string, status = 400) {
		super(message);
		this.name = 'AppVncError';
		this.status = status;
	}
}

interface AppVncSessionRecord {
	id: string;
	agentId: string;
	active: boolean;
	createdAt: Date;
	lastUpdatedAt?: Date;
	lastSequence?: number;
	settings: AppVncSessionSettings;
	metadata?: AppVncSessionMetadata;
	cursor?: AppVncCursorState;
	history: AppVncFramePacket[];
	inputSequence: number;
}

interface AppVncSubscriber {
	controller: ReadableStreamDefaultController<Uint8Array>;
	closed: boolean;
	sessionId?: string;
	heartbeat?: ReturnType<typeof setInterval>;
}

function cloneSettings(settings: AppVncSessionSettings): AppVncSessionSettings {
	return { ...settings };
}

function resolveSettings(patch?: AppVncSessionSettingsPatch): AppVncSessionSettings {
	const resolved: AppVncSessionSettings = { ...defaultSettings };
	if (!patch) {
		return resolved;
	}
	applySettings(resolved, patch);
	return resolved;
}

function applySettings(target: AppVncSessionSettings, updates: AppVncSessionSettingsPatch) {
	if (updates.monitor && typeof updates.monitor === 'string') {
		target.monitor = updates.monitor;
	}
	if (updates.quality) {
		if (!qualities.has(updates.quality)) {
			throw new AppVncError('Invalid quality preset', 400);
		}
		target.quality = updates.quality;
	}
	if (typeof updates.captureCursor === 'boolean') {
		target.captureCursor = updates.captureCursor;
	}
	if (typeof updates.clipboardSync === 'boolean') {
		target.clipboardSync = updates.clipboardSync;
	}
	if (typeof updates.blockLocalInput === 'boolean') {
		target.blockLocalInput = updates.blockLocalInput;
	}
	if (typeof updates.heartbeatInterval === 'number' && Number.isFinite(updates.heartbeatInterval)) {
		const value = Math.trunc(updates.heartbeatInterval);
		if (value < 10) {
			throw new AppVncError('Heartbeat interval must be at least 10 seconds', 400);
		}
		target.heartbeatInterval = value;
	}
	if (typeof updates.appId === 'string') {
		const trimmed = updates.appId.trim();
		if (trimmed.length === 0) {
			if ('appId' in target) {
				delete target.appId;
			}
		} else {
			target.appId = trimmed;
		}
	}
	if (typeof updates.windowTitle === 'string') {
		const trimmed = updates.windowTitle.trim();
		if (trimmed.length === 0) {
			if ('windowTitle' in target) {
				delete target.windowTitle;
			}
		} else {
			target.windowTitle = trimmed;
		}
	}
}

function cloneFrame(frame: AppVncFramePacket): AppVncFramePacket {
	return {
		sessionId: frame.sessionId,
		sequence: frame.sequence,
		timestamp: frame.timestamp,
		width: frame.width,
		height: frame.height,
		encoding: frame.encoding,
		image: frame.image,
		cursor: frame.cursor ? { ...frame.cursor } : undefined,
		metadata: frame.metadata ? { ...frame.metadata } : undefined
	};
}

function toSessionState(record: AppVncSessionRecord): AppVncSessionState {
	return {
		sessionId: record.id,
		agentId: record.agentId,
		active: record.active,
		createdAt: record.createdAt.toISOString(),
		lastUpdatedAt: record.lastUpdatedAt?.toISOString(),
		lastSequence: record.lastSequence,
		settings: cloneSettings(record.settings),
		metadata: record.metadata ? { ...record.metadata } : undefined,
		cursor: record.cursor ? { ...record.cursor } : undefined
	};
}

function formatEvent(event: string, payload: unknown): string {
	return `event: ${event}\ndata: ${JSON.stringify(payload)}\n\n`;
}

function validateFrame(frame: AppVncFramePacket) {
	if (!frame || typeof frame !== 'object') {
		throw new AppVncError('Invalid frame payload', 400);
	}
	if (typeof frame.sessionId !== 'string' || frame.sessionId.length === 0) {
		throw new AppVncError('Frame session identifier is required', 400);
	}
	if (typeof frame.timestamp !== 'string' || frame.timestamp.length === 0) {
		throw new AppVncError('Frame timestamp is required', 400);
	}
	if (!Number.isFinite(frame.sequence) || frame.sequence < 0) {
		throw new AppVncError('Frame sequence must be a non-negative number', 400);
	}
	if (!Number.isFinite(frame.width) || frame.width <= 0 || frame.width > MAX_FRAME_WIDTH) {
		throw new AppVncError('Invalid frame width', 400);
	}
	if (!Number.isFinite(frame.height) || frame.height <= 0 || frame.height > MAX_FRAME_HEIGHT) {
		throw new AppVncError('Invalid frame height', 400);
	}
	if (frame.encoding !== 'png' && frame.encoding !== 'jpeg') {
		throw new AppVncError('Unsupported frame encoding', 400);
	}
	if (typeof frame.image !== 'string' || frame.image.length === 0) {
		throw new AppVncError('Frame image payload required', 400);
	}
	if (frame.image.length > MAX_BASE64_SIZE) {
		throw new AppVncError('Frame payload too large', 413);
	}
	if (frame.cursor) {
		if (!Number.isFinite(frame.cursor.x) || !Number.isFinite(frame.cursor.y)) {
			throw new AppVncError('Cursor coordinates must be finite numbers', 400);
		}
		if (typeof frame.cursor.visible !== 'boolean') {
			throw new AppVncError('Cursor visibility must be boolean', 400);
		}
	}
}

export class AppVncManager {
	private sessions = new Map<string, AppVncSessionRecord>();
	private subscribers = new Map<string, Set<AppVncSubscriber>>();

	createSession(agentId: string, settings?: AppVncSessionSettingsPatch): AppVncSessionState {
		const existing = this.sessions.get(agentId);
		if (existing?.active) {
			throw new AppVncError('App VNC session already active', 409);
		}

		const record: AppVncSessionRecord = {
			id: randomUUID(),
			agentId,
			active: true,
			createdAt: new Date(),
			settings: resolveSettings(settings),
			history: [],
			inputSequence: 0
		};

		this.sessions.set(agentId, record);
		this.broadcastSession(agentId);
		return toSessionState(record);
	}

	getSession(agentId: string): AppVncSessionRecord | undefined {
		return this.sessions.get(agentId);
	}

	getSessionState(agentId: string): AppVncSessionState | null {
		const record = this.sessions.get(agentId);
		if (!record) {
			return null;
		}
		return toSessionState(record);
	}

	updateSettings(agentId: string, updates: AppVncSessionSettingsPatch) {
		const record = this.sessions.get(agentId);
		if (!record || !record.active) {
			throw new AppVncError('No active app VNC session', 404);
		}
		applySettings(record.settings, updates);
		record.lastUpdatedAt = new Date();
		this.broadcastSession(agentId);
	}

	dispatchInput(
		agentId: string,
		sessionId: string,
		events: AppVncInputEvent[],
		options: { sequence?: number } = {}
	): { delivered: boolean; sequence: number | null } {
		const record = this.sessions.get(agentId);
		if (!record || !record.active) {
			throw new AppVncError('No active app VNC session', 404);
		}
		if (record.id !== sessionId) {
			throw new AppVncError('Session identifier mismatch', 409);
		}
		if (!Array.isArray(events) || events.length === 0) {
			return { delivered: false, sequence: null };
		}

		const sequence = this.reserveInputSequence(record, options.sequence);
		if (sequence === null) {
			return { delivered: false, sequence: null };
		}

		const burst: AppVncInputBurst = { sessionId, events, sequence };

		let delivered = false;
		try {
			delivered = registry.sendAppVncInput(agentId, burst);
		} catch (err) {
			console.error('Failed to deliver app VNC input burst', err);
		}

		if (!delivered) {
			try {
				const payload: AppVncCommandPayload = {
					action: 'input',
					sessionId,
					events
				};
				registry.queueCommand(agentId, { name: 'app-vnc', payload });
			} catch (err) {
				if (!(err instanceof RegistryError)) {
					console.error('Failed to enqueue app VNC input command fallback', err);
				}
			}
		}

		return { delivered, sequence };
	}

	ingestFrame(agentId: string, frame: AppVncFramePacket) {
		const record = this.sessions.get(agentId);
		if (!record || !record.active) {
			throw new AppVncError('No active app VNC session', 404);
		}
		if (frame.sessionId !== record.id) {
			throw new AppVncError('Session identifier mismatch', 409);
		}

		validateFrame(frame);

		record.lastUpdatedAt = new Date();
		record.lastSequence = frame.sequence;
		if (frame.metadata) {
			record.metadata = { ...frame.metadata };
		}
		if (frame.cursor) {
			record.cursor = { ...frame.cursor };
		}

		const cloned = cloneFrame(frame);
		record.history.push(cloned);
		if (record.history.length > HISTORY_LIMIT) {
			record.history.splice(0, record.history.length - HISTORY_LIMIT);
		}

		this.broadcastSession(agentId);
		this.broadcast(agentId, 'frame', { frame: cloned });
	}

	closeSession(agentId: string) {
		const record = this.sessions.get(agentId);
		if (!record) {
			return;
		}
		record.active = false;
		record.lastUpdatedAt = new Date();
		record.inputSequence = 0;
		record.history = [];
		record.metadata = undefined;
		record.cursor = undefined;
		record.lastSequence = undefined;
		this.broadcastSession(agentId);
		this.broadcast(agentId, 'end', { reason: 'closed' });
	}

	subscribe(agentId: string, sessionId?: string): ReadableStream<Uint8Array> {
		let subscriber: AppVncSubscriber | null = null;
		return new ReadableStream<Uint8Array>({
			start: (controller) => {
				subscriber = { controller, closed: false, sessionId };
				let subscribers = this.subscribers.get(agentId);
				if (!subscribers) {
					subscribers = new Set();
					this.subscribers.set(agentId, subscribers);
				}
				subscribers.add(subscriber);

				const record = this.sessions.get(agentId);
				if (record && (!sessionId || record.id === sessionId)) {
					controller.enqueue(
						encoder.encode(formatEvent('session', { session: toSessionState(record) }))
					);
					for (const frame of record.history) {
						controller.enqueue(encoder.encode(formatEvent('frame', { frame })));
					}
				} else {
					controller.enqueue(encoder.encode(formatEvent('session', { session: null })));
				}

				subscriber.heartbeat = setInterval(() => {
					if (!subscriber || subscriber.closed) {
						return;
					}
					try {
						controller.enqueue(
							encoder.encode(formatEvent('heartbeat', { timestamp: new Date().toISOString() }))
						);
					} catch (err) {
						const message = err instanceof Error ? err.message : String(err);
						if (!/close|abort|cancel/i.test(message)) {
							console.warn('App VNC heartbeat delivery failure', err);
						}
						if (subscriber) {
							this.removeSubscriber(agentId, subscriber);
						}
					}
				}, HEARTBEAT_INTERVAL_MS);
			},
			cancel: () => {
				if (subscriber) {
					this.removeSubscriber(agentId, subscriber);
					subscriber = null;
				}
			}
		});
	}

	private reserveInputSequence(record: AppVncSessionRecord, hint?: number): number | null {
		const current = record.inputSequence ?? 0;
		if (typeof hint === 'number' && Number.isFinite(hint)) {
			const normalized = Math.trunc(hint);
			if (normalized <= current) {
				return null;
			}
			record.inputSequence = normalized;
			return normalized;
		}
		const next = current + 1;
		record.inputSequence = next;
		return next;
	}

	private removeSubscriber(agentId: string, subscriber: AppVncSubscriber) {
		const subscribers = this.subscribers.get(agentId);
		if (!subscribers) {
			return;
		}
		subscribers.delete(subscriber);
		if (subscriber.heartbeat) {
			clearInterval(subscriber.heartbeat);
		}
		subscriber.closed = true;
		if (subscribers.size === 0) {
			this.subscribers.delete(agentId);
		}
	}

	private broadcastSession(agentId: string) {
		const record = this.sessions.get(agentId);
		if (!record) {
			return;
		}
		this.broadcast(agentId, 'session', { session: toSessionState(record) });
	}

	private broadcast(agentId: string, event: string, payload: unknown) {
		const subscribers = this.subscribers.get(agentId);
		if (!subscribers) {
			return;
		}

		let encoded: Uint8Array | null = null;
		for (const subscriber of subscribers) {
			if (subscriber.closed) continue;
			if (event === 'frame' && subscriber.sessionId) {
				const sessionId = (payload as { frame: AppVncFramePacket }).frame.sessionId;
				if (sessionId !== subscriber.sessionId) {
					continue;
				}
			}
			if (!encoded) {
				encoded = encoder.encode(formatEvent(event, payload));
			}
			try {
				subscriber.controller.enqueue(encoded);
			} catch (err) {
				const message = err instanceof Error ? err.message : String(err);
				if (!/close|abort|cancel/i.test(message)) {
					console.warn('Failed to deliver app VNC event', err);
				}
				this.removeSubscriber(agentId, subscriber);
			}
		}
	}
}

export const appVncManager = new AppVncManager();

export { listAppVncApplications } from '$lib/data/app-vnc-apps';

function inferPlatform(os: string | undefined): AppVncVirtualizationPlan['platform'] {
	if (!os) {
		return undefined;
	}
	const normalized = os.trim().toLowerCase();
	if (normalized.includes('windows')) {
		return 'windows';
	}
	if (normalized.includes('linux')) {
		return 'linux';
	}
	if (normalized.includes('mac') || normalized.includes('darwin') || normalized.includes('os x')) {
		return 'macos';
	}
	return undefined;
}

async function resolveVirtualizationPlan(
	agentId: string,
	appId: string,
	platform: AppVncVirtualizationPlan['platform'],
	hints: AppVncVirtualizationHints | undefined
): Promise<AppVncVirtualizationPlan | undefined> {
	if (!platform) {
		return undefined;
	}
	const plan: AppVncVirtualizationPlan = { platform };
	const seeds = appId ? await resolveSeedPlan(agentId, appId, platform) : {};
	const profileSeed = seeds.profileSeed ?? hints?.profileSeeds?.[platform];
	if (profileSeed) {
		plan.profileSeed = profileSeed;
	}
	const dataRoot = seeds.dataRoot ?? hints?.dataRoots?.[platform];
	if (dataRoot) {
		plan.dataRoot = dataRoot;
	}
	if (hints?.environment?.[platform]) {
		plan.environment = { ...hints.environment[platform] };
	}
	if (plan.profileSeed || plan.dataRoot || plan.environment) {
		return plan;
	}
	return undefined;
}

export async function resolveAppVncStartContext(
	agentId: string,
	settings: AppVncSessionSettings
): Promise<{
	application?: AppVncApplicationDescriptor;
	virtualization?: AppVncVirtualizationPlan;
}> {
	const trimmedAppId = typeof settings.appId === 'string' ? settings.appId.trim() : '';
	if (!trimmedAppId) {
		return {};
	}

	const descriptor = findAppVncApplication(trimmedAppId);
	if (!descriptor) {
		return {};
	}

	let platform: AppVncVirtualizationPlan['platform'];
	try {
		const agent = registry.getAgent(agentId);
		platform = inferPlatform(agent.metadata?.os);
	} catch (err) {
		if (err instanceof RegistryError) {
			platform = undefined;
		} else {
			throw err;
		}
	}

	const virtualization = await resolveVirtualizationPlan(
		agentId,
		descriptor.id,
		platform,
		descriptor.virtualization
	);

	return {
		application: descriptor,
		virtualization
	};
}
