import { createHash, randomBytes, randomUUID, timingSafeEqual } from 'crypto';
import type {
	AudioDeviceInventory,
	AudioDeviceInventoryState,
	AudioDirection,
	AudioSessionState,
	AudioStreamChunk,
	AudioStreamFormat,
	AudioStreamTransport,
	AudioUploadTrack
} from '$lib/types/audio';
import { join } from 'node:path';
import { mkdir, stat, unlink, readFile } from 'node:fs/promises';
import {
	AUDIO_STREAM_SUBPROTOCOL,
	AUDIO_STREAM_TOKEN_HEADER
} from '../../../../../shared/constants/protocol';

const encoder = new TextEncoder();
const AUDIO_STREAM_TOKEN_TTL_MS = 60_000;
export const MAX_AUDIO_STREAM_BASE64_LENGTH = 1 * 1024 * 1024; // 1 MiB
const RFC3339_REGEX = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/;
const BASE64_REGEX = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}(?:==)?|[A-Za-z0-9+/]{3}=)?$/;

function hashStreamToken(token: string): string {
	const hash = createHash('sha256');
	hash.update(token, 'utf-8');
	return hash.digest('hex');
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

function generateStreamToken(): { token: string; hash: string; expiresAt: number } {
	const token = randomBytes(32).toString('hex');
	return { token, hash: hashStreamToken(token), expiresAt: Date.now() + AUDIO_STREAM_TOKEN_TTL_MS };
}

function cloneTransportState(
	transport: AudioStreamTransport | undefined
): AudioStreamTransport | undefined {
	if (!transport) {
		return undefined;
	}
	const { transport: kind, url, protocol } = transport;
	return {
		transport: kind,
		url,
		protocol
	} satisfies AudioStreamTransport;
}

export class AudioBridgeError extends Error {
	status: number;

	constructor(message: string, status = 400) {
		super(message);
		this.name = 'AudioBridgeError';
		this.status = status;
	}
}

interface AudioSubscriber {
	controller: ReadableStreamDefaultController<Uint8Array>;
	closed: boolean;
}

interface AudioSessionRecord {
	id: string;
	agentId: string;
	direction: AudioDirection;
	deviceId?: string;
	deviceLabel?: string;
	format: AudioStreamFormat;
	startedAt: Date;
	lastUpdatedAt?: Date;
	lastSequence?: number;
	active: boolean;
	subscribers: Set<AudioSubscriber>;
	transport?: AudioStreamTransport;
	streamTokenHash?: string;
	streamTokenExpiresAt?: number;
	streamSocket?: WebSocket;
}

interface AudioUploadRecord {
	id: string;
	agentId: string;
	storedName: string;
	originalName: string;
	size: number;
	contentType?: string;
	uploadedAt: Date;
}

function cloneInventory(input: AudioDeviceInventory): AudioDeviceInventory {
	return {
		inputs: input.inputs.map((item) => ({ ...item })),
		outputs: input.outputs.map((item) => ({ ...item })),
		capturedAt: input.capturedAt,
		requestId: input.requestId
	} satisfies AudioDeviceInventory;
}

function encodeEvent(event: string, payload: unknown): Uint8Array {
	return encoder.encode(`event: ${event}\ndata: ${JSON.stringify(payload)}\n\n`);
}

export interface WavInfo {
	channels: number;
	sampleRate: number;
	bitsPerSample: number;
	dataOffset: number;
	dataSize: number;
}

export function parseWavHeader(buffer: Buffer): WavInfo | null {
	if (buffer.length < 44) return null;
	if (buffer.toString('ascii', 0, 4) !== 'RIFF') return null;
	if (buffer.toString('ascii', 8, 12) !== 'WAVE') return null;
	if (buffer.toString('ascii', 12, 16) !== 'fmt ') return null;

	const formatSize = buffer.readUInt32LE(16);
	const channels = buffer.readUInt16LE(22);
	const sampleRate = buffer.readUInt32LE(24);
	const bitsPerSample = buffer.readUInt16LE(34);

	let dataOffset = 20 + formatSize;
	while (dataOffset < buffer.length - 8) {
		if (buffer.toString('ascii', dataOffset, dataOffset + 4) === 'data') {
			const dataSize = buffer.readUInt32LE(dataOffset + 4);
			return {
				channels,
				sampleRate,
				bitsPerSample,
				dataOffset: dataOffset + 8,
				dataSize
			};
		}
		const chunkSize = buffer.readUInt32LE(dataOffset + 4);
		dataOffset += 8 + chunkSize;
	}

	return null;
}

export function createWavHeader(info: WavInfo): Buffer {
	const header = Buffer.alloc(44);
	header.write('RIFF', 0);
	header.writeUInt32LE(36 + info.dataSize, 4);
	header.write('WAVE', 8);
	header.write('fmt ', 12);
	header.writeUInt32LE(16, 16); // fmt chunk size
	header.writeUInt16LE(1, 20); // PCM format
	header.writeUInt16LE(info.channels, 22);
	header.writeUInt32LE(info.sampleRate, 24);
	header.writeUInt32LE((info.sampleRate * info.channels * info.bitsPerSample) / 8, 28);
	header.writeUInt16LE((info.channels * info.bitsPerSample) / 8, 32);
	header.writeUInt16LE(info.bitsPerSample, 34);
	header.write('data', 36);
	header.writeUInt32LE(info.dataSize, 40);
	return header;
}

function toSessionState(record: AudioSessionRecord): AudioSessionState {
	return {
		sessionId: record.id,
		agentId: record.agentId,
		deviceId: record.deviceId,
		deviceLabel: record.deviceLabel,
		direction: record.direction,
		format: { ...record.format },
		startedAt: record.startedAt.toISOString(),
		lastUpdatedAt: record.lastUpdatedAt?.toISOString(),
		lastSequence: record.lastSequence,
		active: record.active,
		transport: cloneTransportState(record.transport)
	} satisfies AudioSessionState;
}

export class AudioBridgeManager {
	private inventories = new Map<string, AudioDeviceInventory>();
	private pendingInventory = new Map<string, Set<string>>();
	private sessions = new Map<string, AudioSessionRecord>();
	private uploads = new Map<string, Map<string, AudioUploadRecord>>();
	private uploadDirectory = join(process.cwd(), '.data', 'audio-uploads');

	async ensureUploadDirectory(agentId: string) {
		const directory = join(this.uploadDirectory, agentId);
		await mkdir(directory, { recursive: true });
		return directory;
	}

	async getUploadPCM(
		agentId: string,
		uploadId: string
	): Promise<{ data: Buffer; format: AudioStreamFormat } | null> {
		const record = this.getUpload(agentId, uploadId);
		if (!record) return null;

		const path = join(this.uploadDirectory, agentId, record.storedName);
		try {
			const buffer = await readFile(path);
			const info = parseWavHeader(buffer);
			if (info) {
				return {
					data: buffer.subarray(info.dataOffset, info.dataOffset + info.dataSize),
					format: {
						encoding: 'pcm16',
						sampleRate: info.sampleRate,
						channels: info.channels
					}
				};
			}
			return {
				data: buffer,
				format: {
					encoding: 'pcm16',
					sampleRate: 48000,
					channels: 1
				}
			};
		} catch {
			return null;
		}
	}

	markInventoryRequest(agentId: string, requestId: string) {
		const trimmed = requestId?.trim();
		if (!trimmed) {
			return;
		}
		if (!this.pendingInventory.has(agentId)) {
			this.pendingInventory.set(agentId, new Set());
		}
		this.pendingInventory.get(agentId)?.add(trimmed);
	}

	updateInventory(agentId: string, inventory: AudioDeviceInventory) {
		if (!inventory) {
			throw new AudioBridgeError('Invalid audio inventory payload', 400);
		}
		this.inventories.set(agentId, cloneInventory(inventory));
		if (inventory.requestId) {
			const pending = this.pendingInventory.get(agentId);
			pending?.delete(inventory.requestId);
			if (pending && pending.size === 0) {
				this.pendingInventory.delete(agentId);
			}
		}
	}

	getInventoryState(agentId: string): AudioDeviceInventoryState {
		const inventory = this.inventories.get(agentId);
		const pending = this.pendingInventory.get(agentId);
		return {
			inventory: inventory ? cloneInventory(inventory) : null,
			pending: Boolean(pending && pending.size > 0)
		} satisfies AudioDeviceInventoryState;
	}

	createSession(
		agentId: string,
		options: {
			direction: AudioDirection;
			deviceId?: string;
			deviceLabel?: string;
			format: AudioStreamFormat;
			sessionId?: string;
		}
	): AudioSessionState {
		if (!options) {
			throw new AudioBridgeError('Missing audio session configuration', 400);
		}

		const direction = options.direction ?? 'input';
		if (direction !== 'input' && direction !== 'output') {
			throw new AudioBridgeError('Unsupported audio direction', 400);
		}

		const existing = this.sessions.get(agentId);
		if (existing && existing.active) {
			throw new AudioBridgeError('An audio session is already active', 409);
		}

		const record: AudioSessionRecord = {
			id: options.sessionId?.trim() || randomUUID(),
			agentId,
			direction,
			deviceId: options.deviceId?.trim() || undefined,
			deviceLabel: options.deviceLabel?.trim() || undefined,
			format: { ...options.format },
			startedAt: new Date(),
			lastUpdatedAt: new Date(),
			active: true,
			subscribers: new Set()
		};

		this.sessions.set(agentId, record);
		return toSessionState(record);
	}

	prepareBinaryTransport(
		agentId: string,
		sessionId: string
	): {
		command: AudioStreamTransport;
		state: AudioStreamTransport;
	} {
		const record = this.sessions.get(agentId);
		if (!record || record.id !== sessionId) {
			throw new AudioBridgeError('Audio session not found', 404);
		}
		if (!record.active) {
			throw new AudioBridgeError('Audio session is not active', 409);
		}

		const url = `/api/agents/${agentId}/audio/ingest?sessionId=${encodeURIComponent(record.id)}`;
		const baseTransport: AudioStreamTransport = {
			transport: 'websocket',
			url,
			protocol: AUDIO_STREAM_SUBPROTOCOL
		} satisfies AudioStreamTransport;

		this.disconnectBinaryStream(record, { code: 1012, reason: 'Stream renegotiated' });

		const generated = generateStreamToken();
		record.streamTokenHash = generated.hash;
		record.streamTokenExpiresAt = generated.expiresAt;
		record.transport = { ...baseTransport } satisfies AudioStreamTransport;

		const command: AudioStreamTransport = {
			...baseTransport,
			headers: {
				[AUDIO_STREAM_TOKEN_HEADER]: generated.token
			}
		} satisfies AudioStreamTransport;

		this.broadcast(record, 'session', toSessionState(record));

		return { command, state: record.transport };
	}

	getSessionState(agentId: string): AudioSessionState | null {
		const record = this.sessions.get(agentId);
		if (!record) {
			return null;
		}
		return toSessionState(record);
	}

	listUploads(agentId: string): AudioUploadTrack[] {
		const uploads = this.uploads.get(agentId);
		if (!uploads) {
			return [];
		}
		return Array.from(uploads.values()).map((upload) => ({
			...upload,
			uploadedAt: upload.uploadedAt.toISOString(),
			filename: upload.originalName,
			downloadUrl: `/api/agents/${agentId}/audio/uploads/${upload.id}`
		}));
	}

	registerUpload(agentId: string, upload: AudioUploadRecord) {
		if (!this.uploads.has(agentId)) {
			this.uploads.set(agentId, new Map());
		}
		this.uploads.get(agentId)?.set(upload.id, upload);
	}

	getUpload(agentId: string, uploadId: string): AudioUploadRecord | null {
		const uploads = this.uploads.get(agentId);
		if (!uploads) {
			return null;
		}
		return uploads.get(uploadId) ?? null;
	}

	async removeUpload(agentId: string, uploadId: string) {
		const uploads = this.uploads.get(agentId);
		if (!uploads) {
			return;
		}
		const record = uploads.get(uploadId);
		if (!record) {
			return;
		}
		const directory = await this.ensureUploadDirectory(agentId);
		const path = join(directory, record.storedName);
		try {
			const file = await stat(path);
			if (file.isFile()) {
				await unlink(path);
			}
		} catch {
			// ignore
		}
		uploads.delete(uploadId);
		if (uploads.size === 0) {
			this.uploads.delete(agentId);
		}
	}

	closeSession(agentId: string, sessionId: string): AudioSessionState | null {
		const record = this.sessions.get(agentId);
		if (!record || record.id !== sessionId) {
			return this.getSessionState(agentId);
		}

		this.disconnectBinaryStream(record, { code: 1011, reason: 'Audio session closed' });
		record.streamTokenHash = undefined;
		record.streamTokenExpiresAt = undefined;
		record.transport = undefined;

		if (record.active) {
			record.active = false;
			record.lastUpdatedAt = new Date();
			this.broadcast(record, 'stopped', { sessionId: record.id });
			this.closeSubscribers(record);
		}

		return toSessionState(record);
	}

	ingestChunk(agentId: string, chunk: AudioStreamChunk) {
		if (!chunk || typeof chunk.sessionId !== 'string' || chunk.sessionId.trim() === '') {
			throw new AudioBridgeError('Audio chunk session identifier is required', 400);
		}

		const sessionId = chunk.sessionId.trim();
		const record = this.sessions.get(agentId);
		if (!record || record.id !== sessionId) {
			throw new AudioBridgeError('No active audio session for this agent', 404);
		}

		if (!record.active) {
			throw new AudioBridgeError('Audio session is not active', 409);
		}

		if (
			!Number.isFinite(chunk.sequence) ||
			chunk.sequence < 0 ||
			!Number.isInteger(chunk.sequence)
		) {
			throw new AudioBridgeError('Invalid audio chunk sequence', 400);
		}

		if (typeof chunk.timestamp !== 'string' || chunk.timestamp.trim() === '') {
			throw new AudioBridgeError('Invalid audio chunk timestamp', 400);
		}

		const timestamp = chunk.timestamp.trim();
		if (!RFC3339_REGEX.test(timestamp) || Number.isNaN(Date.parse(timestamp))) {
			throw new AudioBridgeError('Invalid audio chunk timestamp', 400);
		}

		if (!chunk.format || typeof chunk.format !== 'object') {
			throw new AudioBridgeError('Invalid audio chunk format', 400);
		}

		const { encoding, sampleRate, channels } = chunk.format;
		if (typeof encoding !== 'string' || encoding.trim() === '') {
			throw new AudioBridgeError('Invalid audio chunk format encoding', 400);
		}

		if (encoding !== 'pcm16') {
			throw new AudioBridgeError('Unsupported audio encoding', 400);
		}

		if (!Number.isFinite(sampleRate) || sampleRate <= 0) {
			throw new AudioBridgeError('Invalid audio sample rate', 400);
		}

		if (!Number.isInteger(channels) || channels <= 0) {
			throw new AudioBridgeError('Invalid audio channel count', 400);
		}

		if (typeof chunk.data !== 'string' && !((chunk.data as unknown) instanceof Uint8Array)) {
			throw new AudioBridgeError('Audio chunk data must be a base64 string or binary buffer', 400);
		}

		const base64Data =
			typeof chunk.data === 'string' ? chunk.data : Buffer.from(chunk.data).toString('base64');

		if (base64Data.length > MAX_AUDIO_STREAM_BASE64_LENGTH) {
			throw new AudioBridgeError('Audio chunk payload exceeds maximum size', 400);
		}

		if (typeof chunk.data === 'string' && chunk.data.length > 0 && !BASE64_REGEX.test(chunk.data)) {
			throw new AudioBridgeError('Audio chunk data must be a base64 string', 400);
		}

		record.lastSequence = chunk.sequence;
		record.lastUpdatedAt = new Date();
		this.broadcast(record, 'chunk', {
			sessionId: record.id,
			sequence: chunk.sequence,
			timestamp,
			format: { ...chunk.format },
			data: base64Data
		});
	}

	subscribe(agentId: string, sessionId: string): ReadableStream<Uint8Array> {
		const record = this.sessions.get(agentId);
		if (!record || record.id !== sessionId) {
			throw new AudioBridgeError('Audio session not found', 404);
		}
		if (!record.active) {
			throw new AudioBridgeError('Audio session is not active', 409);
		}

		let subscriber: AudioSubscriber;

		return new ReadableStream<Uint8Array>({
			start: (controller) => {
				subscriber = { controller, closed: false } satisfies AudioSubscriber;
				record.subscribers.add(subscriber);
				controller.enqueue(encodeEvent('session', toSessionState(record)));
			},
			cancel: () => {
				if (subscriber && !subscriber.closed) {
					subscriber.closed = true;
					record.subscribers.delete(subscriber);
				}
			}
		});
	}

	private disconnectBinaryStream(
		record: AudioSessionRecord,
		options: { code?: number; reason?: string } = {}
	) {
		const socket = record.streamSocket;
		if (!socket) {
			return;
		}
		record.streamSocket = undefined;
		try {
			socket.close(options.code ?? 1011, options.reason ?? 'Audio stream closed');
		} catch {
			// ignore
		}
	}

	private validateStreamToken(record: AudioSessionRecord, token: string | null | undefined) {
		if (!token || token.trim() === '') {
			throw new AudioBridgeError('Missing audio stream token', 401);
		}
		if (!record.streamTokenHash) {
			throw new AudioBridgeError('Audio stream token not negotiated', 401);
		}
		if (!record.streamTokenExpiresAt || Date.now() >= record.streamTokenExpiresAt) {
			record.streamTokenHash = undefined;
			record.streamTokenExpiresAt = undefined;
			throw new AudioBridgeError('Audio stream token expired', 401);
		}

		const incoming = hashStreamToken(token);
		if (!timingSafeEqualHex(record.streamTokenHash, incoming)) {
			throw new AudioBridgeError('Invalid audio stream token', 401);
		}

		record.streamTokenExpiresAt = Date.now() + AUDIO_STREAM_TOKEN_TTL_MS;
	}

	private handleBinaryPayload(agentId: string, record: AudioSessionRecord, payload: unknown) {
		if (payload == null) {
			return;
		}

		let buffer: Buffer;
		if (typeof Buffer !== 'undefined' && Buffer.isBuffer(payload)) {
			buffer = payload;
		} else if (payload instanceof ArrayBuffer) {
			buffer = Buffer.from(payload);
		} else if (ArrayBuffer.isView(payload)) {
			buffer = Buffer.from(payload.buffer, payload.byteOffset, payload.byteLength);
		} else if (typeof payload === 'string') {
			buffer = Buffer.from(payload, 'utf-8');
		} else {
			throw new AudioBridgeError('Unsupported audio stream payload', 400);
		}

		const newlineIndex = buffer.indexOf(0x0a);
		if (newlineIndex <= 0) {
			throw new AudioBridgeError('Malformed audio stream frame', 400);
		}

		const headerRaw = buffer.subarray(0, newlineIndex).toString('utf-8');
		let header: {
			sessionId?: string;
			sequence?: number | string;
			timestamp?: string;
			format?: AudioStreamFormat;
		};
		try {
			header = JSON.parse(headerRaw) as typeof header;
		} catch {
			throw new AudioBridgeError('Invalid audio stream frame header', 400);
		}

		const payloadData = buffer.subarray(newlineIndex + 1);
		const base64Data = payloadData.length > 0 ? payloadData.toString('base64') : '';

		if (base64Data.length > MAX_AUDIO_STREAM_BASE64_LENGTH) {
			throw new AudioBridgeError('Audio chunk payload exceeds maximum size', 400);
		}

		let sequence: number | undefined;
		if (typeof header.sequence === 'number' && Number.isFinite(header.sequence)) {
			sequence = header.sequence;
		} else if (typeof header.sequence === 'string') {
			const parsed = Number.parseInt(header.sequence, 10);
			if (Number.isFinite(parsed)) {
				sequence = parsed;
			}
		}

		const chunk: AudioStreamChunk = {
			sessionId: header.sessionId?.trim() || record.id,
			sequence: sequence ?? (record.lastSequence ?? 0) + 1,
			timestamp:
				typeof header.timestamp === 'string' && header.timestamp.trim() !== ''
					? header.timestamp
					: new Date().toISOString(),
			format: header.format ? { ...record.format, ...header.format } : { ...record.format },
			data: base64Data
		} satisfies AudioStreamChunk;

		this.ingestChunk(agentId, chunk);
	}

	attachBinaryStream(agentId: string, sessionId: string, token: string | null, socket: WebSocket) {
		const record = this.sessions.get(agentId);
		if (!record || record.id !== sessionId) {
			throw new AudioBridgeError('Audio session not found', 404);
		}
		if (!record.active) {
			throw new AudioBridgeError('Audio session is not active', 409);
		}

		this.validateStreamToken(record, token);
		this.disconnectBinaryStream(record, { code: 1012, reason: 'Stream replaced' });

		const acceptingSocket = socket as unknown as {
			accept?: (options?: { protocol?: string }) => void;
		};
		if (typeof acceptingSocket.accept === 'function') {
			try {
				acceptingSocket.accept({
					protocol: record.transport?.protocol ?? AUDIO_STREAM_SUBPROTOCOL
				});
			} catch {
				// ignore
			}
		}

		record.streamSocket = socket;

		const handleClose = () => {
			if (record.streamSocket === socket) {
				record.streamSocket = undefined;
			}
		};

		const handleMessage = (event: MessageEvent) => {
			try {
				this.handleBinaryPayload(agentId, record, event.data);
			} catch (err) {
				handleClose();
				const reason = err instanceof AudioBridgeError ? err.message : 'Audio stream error';
				try {
					socket.close(1011, reason);
				} catch {
					// ignore
				}
			}
		};

		const handleCloseEvent = () => {
			handleClose();
		};

		const handleErrorEvent = () => {
			handleClose();
		};

		if (typeof socket.addEventListener === 'function') {
			socket.addEventListener('message', handleMessage);
			socket.addEventListener('close', handleCloseEvent);
			socket.addEventListener('error', handleErrorEvent);
		} else {
			socket.onmessage = handleMessage;
			socket.onclose = handleCloseEvent;
			socket.onerror = handleErrorEvent;
		}
	}

	private broadcast(record: AudioSessionRecord, event: string, payload: unknown) {
		if (record.subscribers.size === 0) {
			return;
		}
		const data = encodeEvent(event, payload);
		for (const subscriber of record.subscribers) {
			try {
				subscriber.controller.enqueue(data);
			} catch {
				subscriber.closed = true;
			}
		}

		for (const subscriber of [...record.subscribers]) {
			if (subscriber.closed) {
				try {
					subscriber.controller.close();
				} catch {
					// ignore
				}
				record.subscribers.delete(subscriber);
			}
		}
	}

	private closeSubscribers(record: AudioSessionRecord) {
		for (const subscriber of record.subscribers) {
			try {
				subscriber.controller.close();
			} catch {
				// ignore
			}
		}
		record.subscribers.clear();
	}
}

export const audioBridgeManager = new AudioBridgeManager();
