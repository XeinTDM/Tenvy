import { randomUUID } from 'crypto';
import type { PluginManifest } from '../../../../../shared/types/plugin-manifest';
import remoteDesktopEngineManifestJson from '../../../../../shared/pluginmanifest/remote-desktop-engine.json';
import type {
	RemoteDesktopEncoder,
	RemoteDesktopFrameMetrics,
	RemoteDesktopFramePacket,
	RemoteDesktopDeltaRect,
	RemoteDesktopHardwarePreference,
	RemoteDesktopInputBurst,
	RemoteDesktopInputEvent,
	RemoteDesktopMediaSample,
	RemoteDesktopMonitor,
	RemoteDesktopSessionNegotiationRequest,
	RemoteDesktopSessionNegotiationResponse,
	RemoteDesktopSessionState,
	RemoteDesktopSettings,
	RemoteDesktopSettingsPatch,
	RemoteDesktopTransport,
	RemoteDesktopTransportCapability,
	RemoteDesktopTransportDiagnostics,
	RemoteDesktopStreamMediaMessage,
	RemoteDesktopWebRTCICEServer,
	RemoteDesktopVideoClip,
	RemoteDesktopVideoFrame
} from '$lib/types/remote-desktop';
import { registry } from './store';
import { WebRTCPipeline } from '$lib/streams/webrtc';
import {
	remoteDesktopInputService,
	type RemoteDesktopQuicDeliveryResult
} from './remote-desktop-input';
import { Buffer } from 'node:buffer';
import { encode as encodeMsgpack } from '@msgpack/msgpack';

const remoteDesktopPluginManifest = remoteDesktopEngineManifestJson as PluginManifest;
export const remoteDesktopEnginePluginId =
	remoteDesktopPluginManifest?.id?.trim() || 'remote-desktop-engine';
export const requiredRemoteDesktopPluginVersion =
	remoteDesktopPluginManifest?.version?.trim() || '';

const encoder = new TextEncoder();
const HEARTBEAT_INTERVAL_MS = 15_000;
const HISTORY_LIMIT = 30;
const MAX_FRAME_WIDTH = 8_192;
const MAX_FRAME_HEIGHT = 8_192;
const MAX_MONITORS = 16;
const MAX_DELTA_RECTS = 512;
const MAX_CLIP_FRAMES = 60;
const MAX_BASE64_PAYLOAD = 16 * 1024 * 1024; // 16 MiB
const DIAGNOSTICS_INTERVAL_MS = 1_000;

const defaultSettings: RemoteDesktopSettings = Object.freeze({
	quality: 'auto',
	monitor: 0,
	mouse: true,
	keyboard: true,
	mode: 'video',
	encoder: 'auto',
	transport: 'webrtc',
	hardware: 'auto',
	targetBitrateKbps: undefined
});

const defaultMonitors: readonly RemoteDesktopMonitor[] = Object.freeze([
	{ id: 0, label: 'Primary', width: 1280, height: 720 }
]);

const qualities = new Set<RemoteDesktopSettings['quality']>(['auto', 'high', 'medium', 'low']);
const modes = new Set<RemoteDesktopSettings['mode']>(['images', 'video']);
const encoders = new Set<RemoteDesktopEncoder>(['auto', 'hevc', 'avc', 'jpeg']);
const transports = new Set<RemoteDesktopTransport>(['http', 'webrtc']);
const hardwarePreferences = new Set<RemoteDesktopHardwarePreference>(['auto', 'prefer', 'avoid']);
const preferredCodecs: RemoteDesktopEncoder[] = ['hevc', 'avc', 'jpeg'];

const configuredIceServers = parseConfiguredIceServers();

function parseConfiguredIceServers(): readonly RemoteDesktopWebRTCICEServer[] {
	const raw = process.env.TENVY_REMOTE_DESKTOP_ICE_SERVERS;
	if (!raw) {
		return [];
	}

	try {
		const parsed = JSON.parse(raw) as RemoteDesktopWebRTCICEServer[];
		const normalized = normalizeIceServers(parsed);
		if (normalized.length === 0) {
			return [];
		}
		return Object.freeze(cloneIceServers(normalized));
	} catch (err) {
		console.warn('Failed to parse remote desktop ICE server configuration', err);
		return [];
	}
}

function normalizeIceServers(
	servers?: (RemoteDesktopWebRTCICEServer | Record<string, unknown>)[] | null
): RemoteDesktopWebRTCICEServer[] {
	if (!servers || servers.length === 0) {
		return [];
	}

	const normalized: RemoteDesktopWebRTCICEServer[] = [];
	for (const server of servers) {
		if (!server) continue;

		const urlSource = (server as { urls?: unknown }).urls;
		const urls = Array.isArray(urlSource)
			? urlSource
			: typeof urlSource === 'string'
				? [urlSource]
				: [];

		const cleaned = urls
			.map((url) => (typeof url === 'string' ? url.trim() : ''))
			.filter((url) => url.length > 0);

		if (cleaned.length === 0) {
			continue;
		}

		const entry: RemoteDesktopWebRTCICEServer = { urls: cleaned };

		if (typeof server.username === 'string' && server.username.trim() !== '') {
			entry.username = server.username.trim();
		}
		if (typeof server.credential === 'string' && server.credential.trim() !== '') {
			entry.credential = server.credential.trim();
		}

		const credentialType =
			typeof server.credentialType === 'string'
				? server.credentialType.trim().toLowerCase()
				: undefined;
		if (credentialType === 'oauth') {
			entry.credentialType = 'oauth';
		} else if (credentialType === 'password' || entry.credential) {
			if (entry.credential) {
				entry.credentialType = 'password';
			}
		}

		normalized.push(entry);
	}

	return normalized;
}

function cloneIceServer(server: RemoteDesktopWebRTCICEServer): RemoteDesktopWebRTCICEServer {
	const cloned: RemoteDesktopWebRTCICEServer = { urls: [...server.urls] };
	if (server.username) {
		cloned.username = server.username;
	}
	if (server.credential) {
		cloned.credential = server.credential;
	}
	if (server.credentialType) {
		cloned.credentialType = server.credentialType;
	}
	return cloned;
}

function cloneIceServers(
	servers: readonly RemoteDesktopWebRTCICEServer[]
): RemoteDesktopWebRTCICEServer[] {
	return servers.map((server) => cloneIceServer(server));
}

function resolveIceServers(
	requested?: RemoteDesktopWebRTCICEServer[] | null
): RemoteDesktopWebRTCICEServer[] {
	const normalized = normalizeIceServers(requested);
	if (normalized.length > 0) {
		return cloneIceServers(normalized);
	}
	return cloneIceServers(configuredIceServers);
}

class RemoteDesktopError extends Error {
	status: number;

	constructor(message: string, status = 400) {
		super(message);
		this.name = 'RemoteDesktopError';
		this.status = status;
	}
}

interface RemoteDesktopSessionRecord {
	id: string;
	agentId: string;
	active: boolean;
	createdAt: Date;
	lastUpdatedAt?: Date;
	lastSequence?: number;
	lastDiagnosticsAt?: number;
	settings: RemoteDesktopSettings;
	activeEncoder?: RemoteDesktopEncoder;
	negotiatedCodec?: RemoteDesktopEncoder;
	transport?: RemoteDesktopTransport;
	intraRefresh?: boolean;
	encoderHardware?: string;
	monitors: RemoteDesktopMonitor[];
	metrics?: RemoteDesktopFrameMetrics;
	transportDiagnostics?: RemoteDesktopTransportDiagnostics;
	history: RemoteDesktopHistoryEntry[];
	hasKeyFrame: boolean;
	transportHandle?: RemoteDesktopTransportHandle | null;
	pipeline?: WebRTCPipeline | null;
	inputSequence: number;
}

interface RemoteDesktopSubscriber {
	agentId: string;
	sessionId?: string;
	controller: ReadableStreamDefaultController<Uint8Array>;
	heartbeat?: ReturnType<typeof setInterval>;
	closed: boolean;
	pipeline?: WebRTCPipeline | null;
}

interface RemoteDesktopTransportHandle {
	close(): void;
}

type RemoteDesktopHistoryEntry =
	| { type: 'frame'; frame: RemoteDesktopFramePacket }
	| { type: 'media'; sessionId: string; media: RemoteDesktopMediaSample[] };

function cloneSettings(settings: RemoteDesktopSettings): RemoteDesktopSettings {
	return { ...settings };
}

function cloneMonitors(monitors: readonly RemoteDesktopMonitor[]): RemoteDesktopMonitor[] {
	return monitors.map((monitor) => ({ ...monitor }));
}

function cloneMediaSamples(
	samples: readonly RemoteDesktopMediaSample[]
): RemoteDesktopMediaSample[] {
	return samples.map((sample) => ({ ...sample }));
}

function monitorsEqual(a: readonly RemoteDesktopMonitor[], b: readonly RemoteDesktopMonitor[]) {
	if (a.length !== b.length) return false;
	for (let i = 0; i < a.length; i += 1) {
		const first = a[i];
		const second = b[i];
		if (!second) return false;
		if (
			first.id !== second.id ||
			first.width !== second.width ||
			first.height !== second.height ||
			first.label !== second.label
		) {
			return false;
		}
	}
	return true;
}

function cloneFrame(frame: RemoteDesktopFramePacket): RemoteDesktopFramePacket {
	const cloned: RemoteDesktopFramePacket = { ...frame };

	if (Array.isArray(frame.deltas)) {
		cloned.deltas = frame.deltas.map((delta) => ({ ...delta }));
	}

	if (frame.clip) {
		cloned.clip = {
			durationMs: frame.clip.durationMs,
			frames: frame.clip.frames.map((clipFrame) => ({ ...clipFrame }))
		};
	}

	if (Array.isArray(frame.monitors)) {
		cloned.monitors = cloneMonitors(frame.monitors);
	}

	if (frame.metrics) {
		cloned.metrics = { ...frame.metrics };
	}

	if (Array.isArray(frame.media)) {
		cloned.media = cloneMediaSamples(frame.media);
	}

	return cloned;
}

function isFiniteNumber(value: unknown): value is number {
	return typeof value === 'number' && Number.isFinite(value);
}

function validatePayloadSize(data: unknown, label: string) {
	if (typeof data === 'string') {
		if (data.length > MAX_BASE64_PAYLOAD) {
			throw new RemoteDesktopError(`${label} payload too large`, 413);
		}
		return;
	}
	if (data instanceof Uint8Array) {
		if (data.length > MAX_BASE64_PAYLOAD) {
			throw new RemoteDesktopError(`${label} payload too large`, 413);
		}
		return;
	}
	throw new RemoteDesktopError(`${label} payload must be string or binary`, 400);
}

function validateFramePacket(frame: RemoteDesktopFramePacket) {
	if (!isFiniteNumber(frame.width) || frame.width <= 0 || frame.width > MAX_FRAME_WIDTH) {
		throw new RemoteDesktopError('Invalid frame width', 400);
	}
	if (!isFiniteNumber(frame.height) || frame.height <= 0 || frame.height > MAX_FRAME_HEIGHT) {
		throw new RemoteDesktopError('Invalid frame height', 400);
	}
	if (!isFiniteNumber(frame.sequence)) {
		throw new RemoteDesktopError('Invalid frame sequence number', 400);
	}
	if (typeof frame.encoding !== 'string' || frame.encoding.length === 0) {
		throw new RemoteDesktopError('Frame encoding is required', 400);
	}
	if (typeof frame.timestamp !== 'string' || frame.timestamp.length === 0) {
		throw new RemoteDesktopError('Frame timestamp is required', 400);
	}
	if (frame.encoderHardware !== undefined && typeof frame.encoderHardware !== 'string') {
		throw new RemoteDesktopError('Encoder hardware label must be a string', 400);
	}
	if (frame.intraRefresh !== undefined && typeof frame.intraRefresh !== 'boolean') {
		throw new RemoteDesktopError('Intra-refresh flag must be boolean', 400);
	}

	if (frame.image) {
		validatePayloadSize(frame.image, 'Frame');
	}

	if (frame.deltas) {
		if (!Array.isArray(frame.deltas)) {
			throw new RemoteDesktopError('Frame deltas must be an array', 400);
		}
		if (frame.deltas.length > MAX_DELTA_RECTS) {
			throw new RemoteDesktopError('Too many delta rectangles', 413);
		}
		for (const rect of frame.deltas) {
			if (
				!isFiniteNumber(rect.width) ||
				!isFiniteNumber(rect.height) ||
				rect.width <= 0 ||
				rect.height <= 0 ||
				rect.width > frame.width ||
				rect.height > frame.height
			) {
				throw new RemoteDesktopError('Invalid delta rectangle dimensions', 400);
			}
			if (!isFiniteNumber(rect.x) || !isFiniteNumber(rect.y)) {
				throw new RemoteDesktopError('Invalid delta rectangle offset', 400);
			}
			if (typeof rect.encoding !== 'string' || rect.encoding.length === 0) {
				throw new RemoteDesktopError('Delta rectangle encoding is required', 400);
			}
			validatePayloadSize(rect.data, 'Delta rectangle');
		}
	}

	if (frame.clip) {
		if (!isFiniteNumber(frame.clip.durationMs) || frame.clip.durationMs < 0) {
			throw new RemoteDesktopError('Invalid clip duration', 400);
		}
		const { frames } = frame.clip;
		if (!Array.isArray(frames)) {
			throw new RemoteDesktopError('Clip frames must be an array', 400);
		}
		if (frames.length > MAX_CLIP_FRAMES) {
			throw new RemoteDesktopError('Clip contains too many frames', 413);
		}
		for (const clipFrame of frames) {
			if (
				!isFiniteNumber(clipFrame.width) ||
				!isFiniteNumber(clipFrame.height) ||
				clipFrame.width <= 0 ||
				clipFrame.height <= 0 ||
				clipFrame.width > frame.width ||
				clipFrame.height > frame.height
			) {
				throw new RemoteDesktopError('Invalid clip frame dimensions', 400);
			}
			if (!isFiniteNumber(clipFrame.offsetMs) || clipFrame.offsetMs < 0) {
				throw new RemoteDesktopError('Invalid clip frame offset', 400);
			}
			if (typeof clipFrame.encoding !== 'string' || clipFrame.encoding.length === 0) {
				throw new RemoteDesktopError('Clip frame encoding is required', 400);
			}
			validatePayloadSize(clipFrame.data, 'Clip frame');
		}
	}

	if (frame.monitors) {
		if (!Array.isArray(frame.monitors)) {
			throw new RemoteDesktopError('Monitor list must be an array', 400);
		}
		if (frame.monitors.length > MAX_MONITORS) {
			throw new RemoteDesktopError('Too many monitors reported', 413);
		}
		for (const monitor of frame.monitors) {
			if (
				!isFiniteNumber(monitor.width) ||
				!isFiniteNumber(monitor.height) ||
				monitor.width <= 0 ||
				monitor.height <= 0 ||
				monitor.width > MAX_FRAME_WIDTH ||
				monitor.height > MAX_FRAME_HEIGHT
			) {
				throw new RemoteDesktopError('Invalid monitor dimensions', 400);
			}
		}
	}

	if (frame.metrics) {
		for (const [key, value] of Object.entries(frame.metrics)) {
			if (value !== undefined && !isFiniteNumber(value)) {
				throw new RemoteDesktopError(`Invalid metric value for ${key}`, 400);
			}
		}
	}

	if (frame.media) {
		validateMediaSamples(frame.media);
	}
}

function validateMediaSamples(samples: RemoteDesktopMediaSample[]) {
	if (!Array.isArray(samples)) {
		throw new RemoteDesktopError('Media samples must be an array', 400);
	}
	for (const sample of samples) {
		if (!sample || typeof sample !== 'object') {
			throw new RemoteDesktopError('Invalid media sample payload', 400);
		}
		if (sample.kind !== 'video' && sample.kind !== 'audio') {
			throw new RemoteDesktopError('Unsupported media sample kind', 400);
		}
		if (typeof sample.codec !== 'string' || sample.codec.length === 0) {
			throw new RemoteDesktopError('Media sample codec is required', 400);
		}
		if (!isFiniteNumber(sample.timestamp)) {
			throw new RemoteDesktopError('Media sample timestamp invalid', 400);
		}
		if (sample.keyFrame !== undefined && typeof sample.keyFrame !== 'boolean') {
			throw new RemoteDesktopError('Media sample keyframe flag invalid', 400);
		}
		if (sample.format && typeof sample.format !== 'string') {
			throw new RemoteDesktopError('Media sample format invalid', 400);
		}
		validatePayloadSize(sample.data, 'Media sample');
	}
}

function coerceFramePacket(payload: unknown): RemoteDesktopFramePacket | null {
	if (!payload || typeof payload !== 'object') {
		return null;
	}

	const candidate = payload as RemoteDesktopFramePacket;
	if (
		typeof candidate.sessionId !== 'string' ||
		typeof candidate.timestamp !== 'string' ||
		typeof candidate.width !== 'number' ||
		typeof candidate.height !== 'number' ||
		typeof candidate.encoding !== 'string'
	) {
		return null;
	}

	const frame: RemoteDesktopFramePacket = { ...candidate };

	const image = ensureBinaryOrBase64((payload as { image?: unknown }).image, true);
	if (image === null) {
		return null;
	}
	if (image !== undefined) {
		frame.image = image;
	}

	if (Array.isArray((payload as { deltas?: unknown }).deltas)) {
		const deltas: RemoteDesktopFramePacket['deltas'] = [];
		for (const entry of (payload as { deltas: unknown[] }).deltas) {
			if (!entry || typeof entry !== 'object') {
				return null;
			}
			const rect = { ...(entry as RemoteDesktopDeltaRect) };
			const data = ensureBinaryOrBase64((entry as { data?: unknown }).data);
			if (data === null || data === undefined) {
				return null;
			}
			rect.data = data;
			deltas.push(rect);
		}
		frame.deltas = deltas;
	}

	if (
		(payload as { clip?: unknown }).clip &&
		typeof (payload as { clip?: unknown }).clip === 'object'
	) {
		const clipSource = (payload as { clip: { durationMs?: unknown; frames?: unknown } }).clip;
		const frames: RemoteDesktopVideoClip['frames'] = [];
		if (Array.isArray((clipSource as { frames?: unknown }).frames)) {
			for (const entry of (clipSource as { frames: unknown[] }).frames) {
				if (!entry || typeof entry !== 'object') {
					return null;
				}
				const clipFrame = { ...(entry as RemoteDesktopVideoFrame) };
				const data = ensureBinaryOrBase64((entry as { data?: unknown }).data);
				if (data === null || data === undefined) {
					return null;
				}
				clipFrame.data = data;
				frames.push(clipFrame);
			}
		}
		frame.clip = {
			durationMs:
				typeof clipSource.durationMs === 'number' && Number.isFinite(clipSource.durationMs)
					? clipSource.durationMs
					: 0,
			frames
		} satisfies RemoteDesktopVideoClip;
	}

	if (Array.isArray((payload as { media?: unknown }).media)) {
		const media: RemoteDesktopMediaSample[] = [];
		for (const entry of (payload as { media: unknown[] }).media) {
			if (!entry || typeof entry !== 'object') {
				return null;
			}
			const sample = { ...(entry as RemoteDesktopMediaSample) };
			const data = ensureBinaryOrBase64((entry as { data?: unknown }).data);
			if (data === null || data === undefined) {
				return null;
			}
			sample.data = data;
			media.push(sample);
		}
		frame.media = media;
	}

	return frame;
}

function ensureBinaryOrBase64(
	value: unknown,
	allowUndefined = false
): string | Uint8Array | undefined | null {
	if (value === undefined) {
		return allowUndefined ? undefined : null;
	}
	if (typeof value === 'string') {
		return value;
	}
	if (value === null) {
		return '';
	}
	if (value instanceof Uint8Array) {
		return value;
	}
	if (value instanceof ArrayBuffer) {
		return new Uint8Array(value);
	}
	if (ArrayBuffer.isView(value)) {
		const view = value as ArrayBufferView;
		return new Uint8Array(view.buffer, view.byteOffset, view.byteLength);
	}
	return null;
}

function convertBuffersToBase64(value: unknown): unknown {
	if (value instanceof Uint8Array) {
		return Buffer.from(value).toString('base64');
	}
	if (Array.isArray(value)) {
		return value.map(convertBuffersToBase64);
	}
	if (value && typeof value === 'object') {
		const result: Record<string, unknown> = {};
		for (const [key, val] of Object.entries(value)) {
			result[key] = convertBuffersToBase64(val);
		}
		return result;
	}
	return value;
}

function preparePayloadForJson(payload: unknown): unknown {
	return convertBuffersToBase64(payload);
}

function appendFrameHistory(record: RemoteDesktopSessionRecord, frame: RemoteDesktopFramePacket) {
	const entry: RemoteDesktopHistoryEntry = { type: 'frame', frame };
	if (frame.keyFrame) {
		record.history = [entry];
		record.hasKeyFrame = true;
		return;
	}

	record.history.push(entry);

	if (!record.hasKeyFrame) {
		const keyIndex = record.history.findIndex(
			(item) => item.type === 'frame' && item.frame.keyFrame
		);
		if (keyIndex >= 0) {
			record.history = record.history.slice(keyIndex);
			record.hasKeyFrame = true;
		}
	}

	trimHistory(record);
}

function appendMediaHistory(
	record: RemoteDesktopSessionRecord,
	sessionId: string,
	media: RemoteDesktopMediaSample[]
) {
	if (!Array.isArray(media) || media.length === 0) {
		return;
	}

	const entry: RemoteDesktopHistoryEntry = {
		type: 'media',
		sessionId,
		media: cloneMediaSamples(media)
	};
	record.history.push(entry);
	trimHistory(record);
}

function countFrameEntries(entries: readonly RemoteDesktopHistoryEntry[]): number {
	let count = 0;
	for (const entry of entries) {
		if (entry.type === 'frame') {
			count += 1;
		}
	}
	return count;
}

function trimHistory(record: RemoteDesktopSessionRecord) {
	const frameCount = countFrameEntries(record.history);
	if (frameCount <= HISTORY_LIMIT) {
		if (frameCount === 0 && record.history.length > HISTORY_LIMIT) {
			record.history = record.history.slice(record.history.length - HISTORY_LIMIT);
		}
		return;
	}

	if (record.hasKeyFrame && record.history[0]?.type === 'frame') {
		const keyframeEntry = record.history[0];
		const tail: RemoteDesktopHistoryEntry[] = [];
		const frameLimit = Math.max(0, HISTORY_LIMIT - 1);
		let framesKept = 0;
		for (let index = record.history.length - 1; index >= 1; index -= 1) {
			const entry = record.history[index];
			if (entry.type === 'frame') {
				if (framesKept >= frameLimit) {
					break;
				}
				framesKept += 1;
				tail.unshift(entry);
			} else {
				if (framesKept >= frameLimit) {
					continue;
				}
				tail.unshift(entry);
			}
		}
		record.history = [keyframeEntry, ...tail];
		return;
	}

	const trimmed: RemoteDesktopHistoryEntry[] = [];
	let framesKept = 0;
	for (let index = record.history.length - 1; index >= 0; index -= 1) {
		const entry = record.history[index];
		if (entry.type === 'frame') {
			if (framesKept >= HISTORY_LIMIT) {
				break;
			}
			framesKept += 1;
			trimmed.unshift(entry);
		} else {
			if (framesKept >= HISTORY_LIMIT) {
				continue;
			}
			trimmed.unshift(entry);
		}
	}
	record.history = trimmed;
}

function resolveSettings(settings?: RemoteDesktopSettingsPatch): RemoteDesktopSettings {
	const resolved = { ...defaultSettings } satisfies RemoteDesktopSettings;
	if (settings) {
		if (settings.quality) {
			if (!qualities.has(settings.quality)) {
				throw new RemoteDesktopError('Invalid quality preset', 400);
			}
			resolved.quality = settings.quality;
		}
		if (settings.mode) {
			if (!modes.has(settings.mode)) {
				throw new RemoteDesktopError('Invalid stream mode', 400);
			}
			resolved.mode = settings.mode;
		}
		if (typeof settings.monitor === 'number' && settings.monitor >= 0) {
			resolved.monitor = Math.floor(settings.monitor);
		}
		if (typeof settings.mouse === 'boolean') {
			resolved.mouse = settings.mouse;
		}
		if (typeof settings.keyboard === 'boolean') {
			resolved.keyboard = settings.keyboard;
		}
		if (settings.transport) {
			if (!transports.has(settings.transport)) {
				throw new RemoteDesktopError('Invalid transport preference', 400);
			}
			resolved.transport = settings.transport;
		}
		if (settings.hardware) {
			if (!hardwarePreferences.has(settings.hardware)) {
				throw new RemoteDesktopError('Invalid hardware acceleration preference', 400);
			}
			resolved.hardware = settings.hardware;
		}
		if (typeof settings.targetBitrateKbps === 'number') {
			const normalized = Math.max(0, Math.trunc(settings.targetBitrateKbps));
			resolved.targetBitrateKbps = normalized > 0 ? normalized : undefined;
		}
		if (settings.encoder) {
			if (!encoders.has(settings.encoder)) {
				throw new RemoteDesktopError('Invalid encoder preference', 400);
			}
			resolved.encoder = settings.encoder;
		}
	}
	return resolved;
}

function applySettings(target: RemoteDesktopSettings, updates: RemoteDesktopSettingsPatch) {
	if (updates.quality) {
		if (!qualities.has(updates.quality)) {
			throw new RemoteDesktopError('Invalid quality preset', 400);
		}
		target.quality = updates.quality;
	}
	if (updates.mode) {
		if (!modes.has(updates.mode)) {
			throw new RemoteDesktopError('Invalid stream mode', 400);
		}
		target.mode = updates.mode;
	}
	if (typeof updates.monitor === 'number') {
		if (updates.monitor < 0) {
			throw new RemoteDesktopError('Monitor index must be non-negative', 400);
		}
		target.monitor = Math.floor(updates.monitor);
	}
	if (typeof updates.mouse === 'boolean') {
		target.mouse = updates.mouse;
	}
	if (typeof updates.keyboard === 'boolean') {
		target.keyboard = updates.keyboard;
	}
	if (updates.encoder) {
		if (!encoders.has(updates.encoder)) {
			throw new RemoteDesktopError('Invalid encoder preference', 400);
		}
		target.encoder = updates.encoder;
	}
	if (updates.transport) {
		if (!transports.has(updates.transport)) {
			throw new RemoteDesktopError('Invalid transport preference', 400);
		}
		target.transport = updates.transport;
	}
	if (updates.hardware) {
		if (!hardwarePreferences.has(updates.hardware)) {
			throw new RemoteDesktopError('Invalid hardware acceleration preference', 400);
		}
		target.hardware = updates.hardware;
	}
	if (typeof updates.targetBitrateKbps === 'number') {
		const normalized = Math.max(0, Math.trunc(updates.targetBitrateKbps));
		target.targetBitrateKbps = normalized > 0 ? normalized : undefined;
	}
}

function formatEvent(event: string, payload: unknown): string {
	return `event: ${event}\ndata: ${JSON.stringify(payload)}\n\n`;
}

function selectCodec(capability?: RemoteDesktopTransportCapability): RemoteDesktopEncoder | null {
	if (!capability || !Array.isArray(capability.codecs)) {
		return null;
	}
	for (const codec of preferredCodecs) {
		if (capability.codecs.includes(codec)) {
			return codec;
		}
	}
	return capability.codecs[0] ?? null;
}

function supportsIntraRefresh(
	capability: RemoteDesktopTransportCapability | undefined,
	requested: boolean | undefined
) {
	if (!capability || !requested) {
		return false;
	}
	return Boolean(capability.features?.intraRefresh);
}

function toSessionState(record: RemoteDesktopSessionRecord): RemoteDesktopSessionState {
	return {
		sessionId: record.id,
		agentId: record.agentId,
		active: record.active,
		createdAt: record.createdAt.toISOString(),
		lastUpdatedAt: record.lastUpdatedAt?.toISOString(),
		lastSequence: record.lastSequence,
		settings: cloneSettings(record.settings),
		activeEncoder: record.activeEncoder,
		negotiatedTransport: record.transport,
		negotiatedCodec: record.negotiatedCodec,
		intraRefresh: record.intraRefresh,
		encoderHardware: record.encoderHardware,
		monitors: cloneMonitors(record.monitors),
		metrics: record.metrics ? { ...record.metrics } : undefined,
		transportDiagnostics: record.transportDiagnostics
			? { ...record.transportDiagnostics }
			: undefined
	};
}

export class RemoteDesktopManager {
	private sessions = new Map<string, RemoteDesktopSessionRecord>();
	private subscribers = new Map<string, Set<RemoteDesktopSubscriber>>();

	createSession(agentId: string, settings?: RemoteDesktopSettingsPatch): RemoteDesktopSessionState {
		const existing = this.sessions.get(agentId);
		if (existing?.active) {
			throw new RemoteDesktopError('Remote desktop session already active', 409);
		}

		const resolved = resolveSettings(settings);
		remoteDesktopInputService.disconnect(agentId);
		const record: RemoteDesktopSessionRecord = {
			id: randomUUID(),
			agentId,
			active: true,
			createdAt: new Date(),
			settings: resolved,
			monitors: cloneMonitors(defaultMonitors),
			history: [],
			hasKeyFrame: false,
			transportHandle: null,
			pipeline: null,
			inputSequence: 0
		};

		this.sessions.set(agentId, record);
		this.broadcastSession(agentId);
		return toSessionState(record);
	}

	getSession(agentId: string): RemoteDesktopSessionRecord | undefined {
		return this.sessions.get(agentId);
	}

	getSessionState(agentId: string): RemoteDesktopSessionState | null {
		const record = this.sessions.get(agentId);
		if (!record) {
			return null;
		}
		return toSessionState(record);
	}

	updateSettings(agentId: string, updates: RemoteDesktopSettingsPatch) {
		const record = this.sessions.get(agentId);
		if (!record || !record.active) {
			throw new RemoteDesktopError('No active remote desktop session', 404);
		}
		applySettings(record.settings, updates);
		if (record.settings.monitor >= record.monitors.length) {
			record.settings.monitor = Math.max(
				0,
				Math.min(record.settings.monitor, record.monitors.length - 1)
			);
		}
		this.broadcastSession(agentId);
	}

	private attachPipelineToSubscriber(agentId: string, sessionId: string, pipeline: WebRTCPipeline) {
		const subscribers = this.subscribers.get(agentId);
		if (!subscribers) return;
		for (const subscriber of subscribers) {
			if (subscriber.sessionId === sessionId) {
				subscriber.pipeline = pipeline;
				break;
			}
		}
	}

	dispatchInput(
		agentId: string,
		sessionId: string,
		events: RemoteDesktopInputEvent[],
		options: { sequence?: number } = {}
	): {
		delivered: boolean;
		sequence: number | null;
		quicDelivered: boolean;
		quicDeliveredAll: boolean;
	} {
		const record = this.sessions.get(agentId);
		if (!record || !record.active) {
			throw new RemoteDesktopError('No active remote desktop session', 404);
		}
		if (record.id !== sessionId) {
			throw new RemoteDesktopError('Session identifier mismatch', 409);
		}
		if (!Array.isArray(events) || events.length === 0) {
			return { delivered: false, sequence: null, quicDelivered: false, quicDeliveredAll: false };
		}

		const sequence = this.reserveInputSequence(record, options.sequence);
		if (sequence === null) {
			return { delivered: false, sequence: null, quicDelivered: false, quicDeliveredAll: false };
		}

		const burst: RemoteDesktopInputBurst = { sessionId, events, sequence };

		let delivered = false;
		let quicDelivery: RemoteDesktopQuicDeliveryResult | null = null;
		try {
			quicDelivery = remoteDesktopInputService.send(agentId, sessionId, burst);
			delivered = quicDelivery.deliveredAll;
		} catch (err) {
			console.warn('Failed to deliver remote desktop input via QUIC service', err);
		}

		const quicDeliveredAll = quicDelivery?.deliveredAll ?? false;
		const quicDeliveredAny = quicDelivery?.deliveredAny ?? false;
		const quicDeliveredEvents = quicDelivery?.deliveredEvents ?? 0;

		let remainingEvents: RemoteDesktopInputEvent[] = burst.events;
		if (quicDeliveredEvents >= burst.events.length) {
			remainingEvents = [];
		} else if (quicDeliveredEvents > 0) {
			remainingEvents = burst.events.slice(quicDeliveredEvents);
		}

		if (!quicDeliveredAll && remainingEvents.length > 0) {
			const fallbackBurst =
				remainingEvents === burst.events ? burst : { ...burst, events: remainingEvents };
			try {
				delivered = registry.sendRemoteDesktopInput(agentId, fallbackBurst);
			} catch (err) {
				console.error('Failed to deliver remote desktop input burst', err);
			}
		}

		if (!delivered) {
			const pendingEvents = remainingEvents.length > 0 ? remainingEvents : burst.events;
			try {
				registry.queueCommand(agentId, {
					name: 'remote-desktop',
					payload: {
						action: 'input',
						sessionId,
						events: pendingEvents
					}
				});
			} catch (err) {
				console.error('Failed to enqueue remote desktop input fallback command', err);
			}
		}

		return {
			delivered,
			sequence,
			quicDelivered: quicDeliveredAny,
			quicDeliveredAll
		};
	}

	async negotiateTransport(
		agentId: string,
		request: RemoteDesktopSessionNegotiationRequest
	): Promise<RemoteDesktopSessionNegotiationResponse> {
		const record = this.sessions.get(agentId);
		if (!record || !record.active) {
			throw new RemoteDesktopError('No active remote desktop session', 404);
		}
		if (request.sessionId !== record.id) {
			throw new RemoteDesktopError('Session identifier mismatch', 409);
		}
		if (!Array.isArray(request.transports) || request.transports.length === 0) {
			throw new RemoteDesktopError('No transport capabilities provided', 400);
		}

		const requestedVersion = request.pluginVersion?.trim() ?? '';
		if (
			requiredRemoteDesktopPluginVersion &&
			requestedVersion !== requiredRemoteDesktopPluginVersion
		) {
			const reason = requestedVersion
				? `Remote desktop engine plugin version ${requiredRemoteDesktopPluginVersion} required (received ${requestedVersion})`
				: `Remote desktop engine plugin version ${requiredRemoteDesktopPluginVersion} required`;
			return {
				accepted: false,
				reason,
				requiredPluginVersion: requiredRemoteDesktopPluginVersion
			} satisfies RemoteDesktopSessionNegotiationResponse;
		}

		const capabilities = request.transports.filter((cap): cap is RemoteDesktopTransportCapability =>
			Boolean(
				cap &&
					typeof cap.transport === 'string' &&
					transports.has(cap.transport as RemoteDesktopTransport)
			)
		);

		if (capabilities.length === 0) {
			throw new RemoteDesktopError('No supported transports offered', 400);
		}

		let selectedTransport: RemoteDesktopTransport = 'http';
		let selectedCodec: RemoteDesktopEncoder | null = null;
		let intraRefresh = false;
		let answer: string | undefined;
		let reason: string | undefined;
		let handle: RemoteDesktopTransportHandle | null = null;
		let pipeline: WebRTCPipeline | null = null;
		let negotiationIceServers: RemoteDesktopWebRTCICEServer[] = [];

		const webrtcCapability = capabilities.find(
			(cap) => cap.transport === 'webrtc' && request.webrtc?.offer
		);
		const supportsBinaryFrames = Boolean(webrtcCapability?.features?.binaryFrames);
		if (webrtcCapability) {
			const codec = selectCodec(webrtcCapability);
			if (codec) {
				try {
					const enableIntra = supportsIntraRefresh(webrtcCapability, request.intraRefresh);
					const result = await this.establishWebRTCTransport(agentId, record, request.webrtc!);
					handle = result.handle;
					pipeline = result.pipeline;
					answer = result.answer;
					negotiationIceServers = result.iceServers;
					selectedTransport = 'webrtc';
					selectedCodec = codec;
					intraRefresh = enableIntra;
				} catch (err) {
					reason = err instanceof Error ? err.message : 'Failed to establish WebRTC transport';
				}
			} else {
				reason = 'No compatible codec for WebRTC transport';
			}
		}

		if (selectedTransport !== 'webrtc') {
			const httpCapability = capabilities.find((cap) => cap.transport === 'http');
			if (!httpCapability) {
				throw new RemoteDesktopError('No fallback transport available', 406);
			}
			selectedCodec = selectCodec(httpCapability) ?? preferredCodecs[preferredCodecs.length - 1];
			intraRefresh = false;
			handle = null;
			pipeline = null;
			selectedTransport = 'http';
		}

		record.transport = selectedTransport;
		record.negotiatedCodec = selectedCodec ?? undefined;
		record.intraRefresh = intraRefresh;
		record.lastUpdatedAt = new Date();

		// Differentiate between Agent and Operator
		const isAgent = Boolean(request.pluginVersion);
		if (isAgent) {
			this.replaceTransportHandle(record, handle, pipeline);
		} else if (pipeline) {
			this.attachPipelineToSubscriber(agentId, request.sessionId, pipeline);
		}

		this.broadcastSession(agentId);

		const response: RemoteDesktopSessionNegotiationResponse = {
			accepted: true,
			transport: selectedTransport,
			codec: selectedCodec ?? undefined,
			intraRefresh,
			requiredPluginVersion: requiredRemoteDesktopPluginVersion || undefined
		};
		const inputNegotiation = remoteDesktopInputService.describe();
		if (inputNegotiation.quic?.enabled) {
			response.input = inputNegotiation;
		}
		if (answer) {
			const responseIce =
				negotiationIceServers.length > 0 ? cloneIceServers(negotiationIceServers) : undefined;
			response.webrtc = {
				answer,
				dataChannel: request.webrtc?.dataChannel,
				iceServers: responseIce
			};
		}
		if (selectedTransport === 'webrtc' && supportsBinaryFrames) {
			response.features = { binaryFrames: true };
		}
		if (reason && selectedTransport !== 'webrtc') {
			response.reason = reason;
		}
		return response;
	}

	closeSession(agentId: string) {
		const record = this.sessions.get(agentId);
		if (!record) {
			return;
		}
		remoteDesktopInputService.disconnect(agentId, record.id);
		record.active = false;
		this.replaceTransportHandle(record, null, null);
		record.lastUpdatedAt = new Date();
		record.inputSequence = 0;
		record.transportDiagnostics = undefined;
		record.lastDiagnosticsAt = undefined;
		this.broadcastSession(agentId);
		this.broadcast(agentId, 'end', { reason: 'closed' });

		record.history = [];
		record.hasKeyFrame = false;
		record.lastSequence = undefined;
		record.metrics = undefined;
		record.activeEncoder = undefined;
		record.negotiatedCodec = undefined;
		record.transport = undefined;
		record.intraRefresh = undefined;
		record.encoderHardware = undefined;
	}

	ingestFrame(agentId: string, frame: RemoteDesktopFramePacket) {
		const record = this.sessions.get(agentId);
		if (!record || !record.active) {
			throw new RemoteDesktopError('No active remote desktop session', 404);
		}
		if (frame.sessionId !== record.id) {
			throw new RemoteDesktopError('Session identifier mismatch', 409);
		}

		validateFramePacket(frame);

		let sessionChanged = false;
		if (frame.transport && transports.has(frame.transport)) {
			if (record.transport !== frame.transport) {
				record.transport = frame.transport;
				sessionChanged = true;
			}
		}

		record.lastSequence = frame.sequence;
		record.lastUpdatedAt = new Date();
		if (frame.metrics) {
			record.metrics = { ...frame.metrics };
		}

		if (frame.encoder && encoders.has(frame.encoder)) {
			record.activeEncoder = frame.encoder;
		}

		if (typeof frame.intraRefresh === 'boolean' && frame.intraRefresh !== record.intraRefresh) {
			record.intraRefresh = frame.intraRefresh;
			sessionChanged = true;
		}

		if (typeof frame.encoderHardware === 'string' && frame.encoderHardware.trim() !== '') {
			const normalizedHardware = frame.encoderHardware.trim();
			if (record.encoderHardware !== normalizedHardware) {
				record.encoderHardware = normalizedHardware;
				sessionChanged = true;
			}
		}

		if (frame.monitors && frame.monitors.length > 0) {
			const next = cloneMonitors(frame.monitors);
			if (!monitorsEqual(record.monitors, next)) {
				record.monitors = next;
				if (record.settings.monitor >= record.monitors.length) {
					record.settings.monitor = Math.max(
						0,
						Math.min(record.settings.monitor, record.monitors.length - 1)
					);
				}
				this.broadcastSession(agentId);
				sessionChanged = false;
			}
		}

		appendFrameHistory(record, cloneFrame(frame));

		if (sessionChanged) {
			this.broadcastSession(agentId);
		}

		this.broadcast(agentId, 'frame', { frame });
	}

	subscribe(agentId: string, sessionId?: string): ReadableStream<Uint8Array> {
		let subscriber: RemoteDesktopSubscriber | null = null;
		return new ReadableStream<Uint8Array>({
			start: (controller) => {
				subscriber = {
					agentId,
					sessionId,
					controller,
					closed: false
				};

				let subscribers = this.subscribers.get(agentId);
				if (!subscribers) {
					subscribers = new Set();
					this.subscribers.set(agentId, subscribers);
				}
				subscribers.add(subscriber);

				const session = this.sessions.get(agentId);
				if (session && subscriber) {
					const sessionChunk = encoder.encode(
						formatEvent('session', { session: preparePayloadForJson(toSessionState(session)) })
					);
					if (!this.enqueueSubscriber(agentId, subscriber, sessionChunk)) {
						subscriber = null;
						return;
					}

					for (const entry of session.history) {
						if (!subscriber || subscriber.closed) {
							return;
						}
						if (entry.type === 'frame') {
							const frame = entry.frame;
							if (sessionId && sessionId !== frame.sessionId) {
								continue;
							}
							const frameChunk = encoder.encode(
								formatEvent('frame', { frame: preparePayloadForJson(frame) })
							);
							if (!this.enqueueSubscriber(agentId, subscriber, frameChunk)) {
								subscriber = null;
								return;
							}
						} else {
							if (sessionId && sessionId !== entry.sessionId) {
								continue;
							}
							const mediaChunk = encoder.encode(
								formatEvent(
									'media',
									preparePayloadForJson({
										sessionId: entry.sessionId,
										media: entry.media
									})
								)
							);
							if (!this.enqueueSubscriber(agentId, subscriber, mediaChunk)) {
								subscriber = null;
								return;
							}
						}
					}
				} else if (subscriber) {
					const sessionChunk = encoder.encode(
						formatEvent('session', {
							session: {
								sessionId: '',
								agentId,
								active: false,
								createdAt: new Date().toISOString(),
								settings: cloneSettings(defaultSettings),
								monitors: cloneMonitors(defaultMonitors)
							}
						})
					);
					if (!this.enqueueSubscriber(agentId, subscriber, sessionChunk)) {
						subscriber = null;
						return;
					}
				}

				if (!subscriber || subscriber.closed) {
					subscriber = null;
					return;
				}

				subscriber.heartbeat = setInterval(() => {
					if (!subscriber || subscriber.closed) {
						if (subscriber?.heartbeat) {
							clearInterval(subscriber.heartbeat);
						}
						return;
					}
					const heartbeatChunk = encoder.encode(`: heartbeat ${Date.now()}\n\n`);
					if (!this.enqueueSubscriber(agentId, subscriber, heartbeatChunk)) {
						subscriber = null;
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

		if (event === 'frame') {
			const frame = (payload as { frame: RemoteDesktopFramePacket }).frame;
			let encoded: Uint8Array | null = null;
			let binaryMsgpack: Uint8Array | null = null;
			for (const subscriber of subscribers) {
				if (subscriber.closed) continue;
				if (subscriber.sessionId && subscriber.sessionId !== frame.sessionId) {
					continue;
				}

				if (subscriber.pipeline) {
					if (!binaryMsgpack) {
						binaryMsgpack = encodeMsgpack(frame);
					}
					if (subscriber.pipeline.sendBinary(binaryMsgpack)) {
						continue;
					}
				}

				if (!encoded) {
					encoded = encoder.encode(formatEvent(event, { frame: preparePayloadForJson(frame) }));
				}
				this.enqueueSubscriber(agentId, subscriber, encoded);
			}
			return;
		}

		if (event === 'media') {
			const mediaPayload = payload as RemoteDesktopStreamMediaMessage;
			if (!Array.isArray(mediaPayload.media) || mediaPayload.media.length === 0) {
				return;
			}
			let encoded: Uint8Array | null = null;
			let binaryMsgpack: Uint8Array | null = null;
			for (const subscriber of subscribers) {
				if (subscriber.closed) continue;
				if (subscriber.sessionId && subscriber.sessionId !== mediaPayload.sessionId) {
					continue;
				}

				if (subscriber.pipeline) {
					if (!binaryMsgpack) {
						binaryMsgpack = encodeMsgpack(mediaPayload);
					}
					if (subscriber.pipeline.sendBinary(binaryMsgpack)) {
						continue;
					}
				}

				if (!encoded) {
					encoded = encoder.encode(formatEvent(event, preparePayloadForJson(mediaPayload)));
				}
				this.enqueueSubscriber(agentId, subscriber, encoded);
			}
			return;
		}

		const data = encoder.encode(formatEvent(event, preparePayloadForJson(payload)));
		for (const subscriber of subscribers) {
			if (subscriber.closed) continue;
			if (subscriber.pipeline) {
				subscriber.pipeline.send(JSON.stringify(preparePayloadForJson(payload)));
			}
			this.enqueueSubscriber(agentId, subscriber, data);
		}
	}

	private enqueueSubscriber(
		agentId: string,
		subscriber: RemoteDesktopSubscriber,
		chunk: Uint8Array
	): boolean {
		if (!chunk || chunk.byteLength === 0 || subscriber.closed) {
			return false;
		}

		try {
			subscriber.controller.enqueue(chunk);
			return true;
		} catch (err) {
			const message = err instanceof Error ? err.message : String(err);
			if (!/close|abort|cancel/i.test(message)) {
				console.warn('Failed to deliver remote desktop event', err);
			}
			this.removeSubscriber(agentId, subscriber);
			return false;
		}
	}

	private replaceTransportHandle(
		record: RemoteDesktopSessionRecord,
		handle: RemoteDesktopTransportHandle | null,
		pipeline: WebRTCPipeline | null = null
	) {
		if (!record) {
			return;
		}

		const previous = record.transportHandle;
		const previousPipeline = record.pipeline;
		record.transportHandle = handle ?? null;
		record.pipeline = pipeline ?? null;
		if (previousPipeline !== record.pipeline) {
			record.lastDiagnosticsAt = undefined;
		}

		if (previous && previous !== handle) {
			try {
				previous.close();
			} catch (err) {
				console.error('Failed to close remote desktop transport', err);
			}
		}
		if (previousPipeline && previousPipeline !== pipeline) {
			try {
				previousPipeline.close();
			} catch (err) {
				console.error('Failed to close remote desktop pipeline', err);
			}
		}
	}

	private reserveInputSequence(record: RemoteDesktopSessionRecord, hint?: number): number | null {
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

	private async establishWebRTCTransport(
		agentId: string,
		record: RemoteDesktopSessionRecord,
		params: NonNullable<RemoteDesktopSessionNegotiationRequest['webrtc']>
	): Promise<{
		handle: RemoteDesktopTransportHandle;
		pipeline: WebRTCPipeline;
		answer: string;
		iceServers: RemoteDesktopWebRTCICEServer[];
	}> {
		const offer = params.offer?.trim();
		if (!offer) {
			throw new RemoteDesktopError('Missing WebRTC offer', 400);
		}

		const iceServers = resolveIceServers(params.iceServers);
		let pipeline: WebRTCPipeline | null = null;
		const result = await WebRTCPipeline.create({
			offer,
			dataChannel: params.dataChannel,
			iceServers,
			onMessage: (payload) => {
				this.handleWebRTCMessage(agentId, record, payload);
			},
			onClose: () => {
				if (pipeline && record.pipeline === pipeline) {
					this.replaceTransportHandle(record, null, null);
					record.transport = 'http';
					record.intraRefresh = false;
					record.lastUpdatedAt = new Date();
					this.broadcastSession(agentId);
				}
			}
		});

		pipeline = result.pipeline;

		const transportHandle: RemoteDesktopTransportHandle = {
			close: () => {
				pipeline?.close();
			}
		};

		record.pipeline = pipeline;

		return {
			handle: transportHandle,
			pipeline,
			answer: result.answer,
			iceServers: result.iceServers
		};
	}

	private handleWebRTCMessage(
		agentId: string,
		record: RemoteDesktopSessionRecord,
		message: RemoteDesktopMediaSample[] | RemoteDesktopFramePacket | string
	) {
		if (Array.isArray(message)) {
			if (message.length === 0) {
				return;
			}
			try {
				validateMediaSamples(message);
			} catch (err) {
				console.error('Failed to validate WebRTC media payload', err);
				return;
			}

			appendMediaHistory(record, record.id, message);
			record.lastUpdatedAt = new Date();
			this.broadcast(agentId, 'media', {
				sessionId: record.id,
				media: cloneMediaSamples(message)
			});
			return;
		}

		if (message && typeof message === 'object') {
			const frame = coerceFramePacket(message);
			if (!frame || frame.sessionId !== record.id) {
				return;
			}
			this.processWebRTCFrame(agentId, record, frame);
			return;
		}

		const payload = typeof message === 'string' ? message.trim() : '';
		if (!payload) {
			return;
		}

		try {
			const frame = coerceFramePacket(JSON.parse(payload));
			if (!frame || frame.sessionId !== record.id) {
				return;
			}
			this.processWebRTCFrame(agentId, record, frame);
		} catch (err) {
			console.error('Failed to process WebRTC frame payload', err);
		}
	}

	private processWebRTCFrame(
		agentId: string,
		record: RemoteDesktopSessionRecord,
		frame: RemoteDesktopFramePacket
	) {
		try {
			this.ingestFrame(agentId, frame);
		} catch (err) {
			if (err instanceof RemoteDesktopError) {
				console.warn('WebRTC frame rejected:', err.message);
			} else {
				console.error('Failed to ingest WebRTC frame', err);
			}
		}

		const currentPipeline = record.pipeline;
		if (!currentPipeline) {
			return;
		}
		const now = Date.now();
		if (record.lastDiagnosticsAt && now - record.lastDiagnosticsAt < DIAGNOSTICS_INTERVAL_MS) {
			return;
		}
		record.lastDiagnosticsAt = now;
		void currentPipeline.collectDiagnostics().then((diagnostics) => {
			if (record.pipeline !== currentPipeline) {
				return;
			}
			if (!diagnostics) {
				record.lastDiagnosticsAt = undefined;
				return;
			}
			record.lastDiagnosticsAt = Date.now();
			const previous = record.transportDiagnostics;
			const next = { ...diagnostics } satisfies RemoteDesktopTransportDiagnostics;
			if (record.encoderHardware) {
				next.hardwareEncoder = record.encoderHardware;
			}
			const changed =
				!previous ||
				previous.transport !== next.transport ||
				previous.codec !== next.codec ||
				previous.currentBitrateKbps !== next.currentBitrateKbps ||
				previous.bandwidthEstimateKbps !== next.bandwidthEstimateKbps ||
				previous.rttMs !== next.rttMs;
			record.transportDiagnostics = next;
			if (changed) {
				record.lastUpdatedAt = new Date();
				this.broadcastSession(agentId);
			}
		});
	}

	removeSubscriber(agentId: string, subscriber: RemoteDesktopSubscriber) {
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
}

export const remoteDesktopManager = new RemoteDesktopManager();
export { RemoteDesktopError };
