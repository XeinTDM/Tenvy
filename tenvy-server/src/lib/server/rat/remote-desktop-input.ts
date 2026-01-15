import { createHash, timingSafeEqual } from 'node:crypto';
import path from 'node:path';
import { readFile } from 'node:fs/promises';
import type {
	RemoteDesktopInputBurst,
	RemoteDesktopInputEvent,
	RemoteDesktopInputNegotiation,
	RemoteDesktopInputQuicConfig,
	RemoteDesktopMouseButton
} from '$lib/types/remote-desktop';

export type RawInputEvent = Record<string, unknown>;

const mouseButtons = new Set<RemoteDesktopMouseButton>(['left', 'middle', 'right']);
const DEFAULT_ALPN = 'tenvy.remote-desktop.input.v1';
const DEFAULT_ADDRESS = process.env.TENVY_QUIC_INPUT_ADDRESS ?? '0.0.0.0';
const DEFAULT_PORT = Number.parseInt(process.env.TENVY_QUIC_INPUT_PORT ?? '0', 10) || 9543;
const MAX_INPUT_BUFFER = 1_048_576;
const MAX_EVENT_BATCH = 256;

const parseTokenValue = (token: unknown): string => {
	return typeof token === 'string' ? token.trim() : '';
};

export function parseQuicTokenConfiguration(value: unknown): string[] {
	if (!value) {
		return [];
	}

	if (Array.isArray(value)) {
		const tokens = value.map((token) => parseTokenValue(token)).filter((token) => token.length > 0);
		return Array.from(new Set(tokens));
	}

	if (typeof value === 'string') {
		const parts = value
			.split(/[\s,]+/)
			.map((token) => token.trim())
			.filter((token) => token.length > 0);
		return Array.from(new Set(parts));
	}

	return [];
}

const hashToken = (token: string): Buffer => {
	return createHash('sha256').update(token, 'utf-8').digest();
};

export function createQuicTokenMatcher(
	tokens: readonly string[]
): (token: string | null | undefined) => boolean {
	if (!tokens || tokens.length === 0) {
		return () => true;
	}

	const digests = tokens.map((token) => hashToken(token));

	return (token) => {
		const trimmed = parseTokenValue(token);
		if (!trimmed) {
			return false;
		}
		const incoming = hashToken(trimmed);
		for (const expected of digests) {
			try {
				if (expected.length === incoming.length && timingSafeEqual(expected, incoming)) {
					return true;
				}
			} catch {
				continue;
			}
		}
		return false;
	};
}

const numberFromUnknown = (value: unknown): number | null => {
	if (typeof value === 'number') {
		return Number.isFinite(value) ? value : null;
	}
	if (typeof value === 'string' && value.trim() !== '') {
		const parsed = Number.parseFloat(value);
		return Number.isFinite(parsed) ? parsed : null;
	}
	return null;
};

const clampMonitorIndex = (value: unknown) => {
	const parsed = numberFromUnknown(value);
	if (parsed === null) return null;
	const normalized = Math.trunc(parsed);
	return normalized >= 0 ? normalized : null;
};

const toBoolean = (value: unknown, fallback = false) => {
	return typeof value === 'boolean' ? value : fallback;
};

const resolveCapturedAt = (value: unknown) => {
	const parsed = numberFromUnknown(value);
	if (parsed === null) {
		return Date.now();
	}
	const normalized = Math.trunc(parsed);
	return normalized >= 0 ? normalized : Date.now();
};

export function sanitizeInputEvent(
	raw: RawInputEvent,
	allowMouse: boolean,
	allowKeyboard: boolean
): RemoteDesktopInputEvent | null {
	const type = typeof raw.type === 'string' ? raw.type : '';
	if (!type) {
		return null;
	}

	if (type === 'mouse-move' || type === 'mouse-button' || type === 'mouse-scroll') {
		if (!allowMouse) {
			return null;
		}
	}
	if (type === 'key' && !allowKeyboard) {
		return null;
	}

	switch (type) {
		case 'mouse-move': {
			const x = numberFromUnknown(raw.x);
			const y = numberFromUnknown(raw.y);
			if (x === null || y === null) {
				return null;
			}
			const event: RemoteDesktopInputEvent = {
				type: 'mouse-move',
				capturedAt: resolveCapturedAt(raw.capturedAt),
				x,
				y,
				normalized: raw.normalized === true
			};
			const monitor = clampMonitorIndex(raw.monitor);
			if (monitor !== null) {
				event.monitor = monitor;
			}
			return event;
		}
		case 'mouse-button': {
			const button =
				typeof raw.button === 'string' ? (raw.button as RemoteDesktopMouseButton) : null;
			if (!button || !mouseButtons.has(button)) {
				return null;
			}
			if (typeof raw.pressed !== 'boolean') {
				return null;
			}
			const event: RemoteDesktopInputEvent = {
				type: 'mouse-button',
				capturedAt: resolveCapturedAt(raw.capturedAt),
				button,
				pressed: raw.pressed
			};
			const monitor = clampMonitorIndex(raw.monitor);
			if (monitor !== null) {
				event.monitor = monitor;
			}
			return event;
		}
		case 'mouse-scroll': {
			const deltaX = numberFromUnknown(raw.deltaX) ?? 0;
			const deltaY = numberFromUnknown(raw.deltaY) ?? 0;
			if (deltaX === 0 && deltaY === 0) {
				return null;
			}
			const event: RemoteDesktopInputEvent = {
				type: 'mouse-scroll',
				capturedAt: resolveCapturedAt(raw.capturedAt),
				deltaX,
				deltaY
			};
			const deltaMode = numberFromUnknown(raw.deltaMode);
			if (deltaMode !== null) {
				event.deltaMode = Math.trunc(deltaMode);
			}
			const monitor = clampMonitorIndex(raw.monitor);
			if (monitor !== null) {
				event.monitor = monitor;
			}
			return event;
		}
		case 'key': {
			if (typeof raw.pressed !== 'boolean') {
				return null;
			}
			const event: RemoteDesktopInputEvent = {
				type: 'key',
				capturedAt: resolveCapturedAt(raw.capturedAt),
				pressed: raw.pressed,
				repeat: toBoolean(raw.repeat, false),
				altKey: toBoolean(raw.altKey, false),
				ctrlKey: toBoolean(raw.ctrlKey, false),
				shiftKey: toBoolean(raw.shiftKey, false),
				metaKey: toBoolean(raw.metaKey, false)
			};
			if (typeof raw.key === 'string') {
				event.key = raw.key;
			}
			if (typeof raw.code === 'string') {
				event.code = raw.code;
			}
			const keyCode = numberFromUnknown(raw.keyCode);
			if (keyCode !== null) {
				event.keyCode = Math.trunc(keyCode);
			}
			return event;
		}
		default:
			return null;
	}
}

export function sanitizeInputEvents(
	events: RawInputEvent[],
	allowMouse: boolean,
	allowKeyboard: boolean
): RemoteDesktopInputEvent[] {
	const sanitized: RemoteDesktopInputEvent[] = [];
	for (const raw of events) {
		if (!raw || typeof raw !== 'object') {
			continue;
		}
		const event = sanitizeInputEvent(raw, allowMouse, allowKeyboard);
		if (event) {
			sanitized.push(event);
		}
	}
	return sanitized;
}

interface RemoteDesktopQuicAgentMessage {
	type?: unknown;
	agentId?: unknown;
	sessionId?: unknown;
	sequence?: unknown;
	events?: unknown;
	token?: unknown;
	timestamp?: unknown;
}

export interface RemoteDesktopQuicInputOptions {
	address?: string;
	port?: number;
	key?: string;
	cert?: string;
	alpn?: string;
	disabled?: boolean;
	tokens?: string[];
}

type QuicSocket = Record<string, unknown>;
type QuicSession = Record<string, unknown>;
type QuicStream = Record<string, unknown>;

interface QuicAgentConnection {
	agentId: string;
	sessionId: string;
	session: QuicSession;
	stream: QuicStream;
}

export interface RemoteDesktopQuicDeliveryResult {
	deliveredAll: boolean;
	deliveredAny: boolean;
	deliveredEvents: number;
	sequence: number | null;
}

export class RemoteDesktopQuicInputService {
	private socket: QuicSocket | null = null;
	private started = false;
	private startPromise: Promise<void> | null = null;
	private connections = new Map<string, QuicAgentConnection>();
	private streamLookup = new WeakMap<object, QuicAgentConnection>();
	private port = DEFAULT_PORT;
	private alpn = DEFAULT_ALPN;
	private address = DEFAULT_ADDRESS;
	private tokenMatcher: (token: string | null | undefined) => boolean = () => true;
	private tokensConfigured = false;

	async start(options: RemoteDesktopQuicInputOptions = {}): Promise<void> {
		this.configureTokens(options);

		if (options.disabled || process.env.TENVY_QUIC_INPUT_DISABLED === '1') {
			return;
		}

		if (this.started) {
			return;
		}

		if (this.startPromise) {
			return this.startPromise;
		}

		const startOperation = (async () => {
			try {
				await this.initialize(options);
			} catch (err) {
				console.warn('Failed to initialize QUIC input service:', err);
				throw err;
			} finally {
				this.startPromise = null;
			}
		})();

		this.startPromise = startOperation;

		try {
			await startOperation;
		} catch {
			// ignore
		}
	}

	describe(): RemoteDesktopInputNegotiation {
		if (!this.started || !this.socket) {
			return {};
		}
		const quic: RemoteDesktopInputQuicConfig = {
			enabled: true,
			port: this.port,
			alpn: this.alpn
		};
		return { quic };
	}

	hasConnection(agentId: string, sessionId?: string): boolean {
		const connection = this.connections.get(agentId);
		if (!connection) {
			return false;
		}
		if (sessionId && connection.sessionId !== sessionId) {
			return false;
		}
		return true;
	}

	send(
		agentId: string,
		sessionId: string,
		burst: RemoteDesktopInputBurst
	): RemoteDesktopQuicDeliveryResult {
		const sequence = typeof burst.sequence === 'number' ? burst.sequence : null;

		if (!agentId || !sessionId) {
			return { deliveredAll: false, deliveredAny: false, deliveredEvents: 0, sequence };
		}
		const connection = this.connections.get(agentId);
		if (!connection || connection.sessionId !== sessionId) {
			return { deliveredAll: false, deliveredAny: false, deliveredEvents: 0, sequence };
		}
		if (!Array.isArray(burst.events) || burst.events.length === 0) {
			return { deliveredAll: false, deliveredAny: false, deliveredEvents: 0, sequence };
		}

		let deliveredEvents = 0;

		for (let index = 0; index < burst.events.length; index += MAX_EVENT_BATCH) {
			const chunk = burst.events.slice(index, index + MAX_EVENT_BATCH);
			const payload = {
				type: 'input',
				sessionId,
				sequence: burst.sequence,
				events: chunk
			};
			const delivered = this.sendMessage(connection.stream, payload);
			if (!delivered) {
				if (deliveredEvents === 0) {
					this.detachConnection(connection, 'write-failed');
				} else {
					this.detachConnection(connection, 'partial-write-failed');
				}
				return {
					deliveredAll: false,
					deliveredAny: deliveredEvents > 0,
					deliveredEvents,
					sequence
				} satisfies RemoteDesktopQuicDeliveryResult;
			}

			deliveredEvents += chunk.length;
		}

		return {
			deliveredAll: true,
			deliveredAny: deliveredEvents > 0,
			deliveredEvents,
			sequence
		} satisfies RemoteDesktopQuicDeliveryResult;
	}

	disconnect(agentId: string, sessionId?: string) {
		const connection = this.connections.get(agentId);
		if (!connection) {
			return;
		}
		if (sessionId && sessionId !== connection.sessionId) {
			return;
		}
		this.detachConnection(connection, 'session-ended');
	}

	private async initialize(options: RemoteDesktopQuicInputOptions): Promise<void> {
		const keySource = options.key ?? process.env.TENVY_QUIC_INPUT_KEY;
		const certSource = options.cert ?? process.env.TENVY_QUIC_INPUT_CERT;

		if (!keySource || !certSource) {
			console.warn('Remote desktop QUIC input disabled: TLS credentials not provided.');
			return;
		}

		const [key, cert] = await Promise.all([
			this.resolveCredential(keySource),
			this.resolveCredential(certSource)
		]);

		if (!key || !cert) {
			console.warn('Remote desktop QUIC input disabled: unable to resolve TLS credentials.');
			return;
		}

		let quicModule: Record<string, unknown> | null = null;
		try {
			quicModule = (await import('node:quic')) as Record<string, unknown>;
		} catch (err) {
			console.warn('Remote desktop QUIC input unavailable: runtime lacks node:quic support.');
			console.debug(err);
			return;
		}

		const createQuicSocket = quicModule?.createQuicSocket as
			| ((options?: Record<string, unknown>) => QuicSocket | null)
			| undefined;
		if (typeof createQuicSocket !== 'function') {
			console.warn('Remote desktop QUIC input unavailable: createQuicSocket not exposed.');
			return;
		}

		const address = options.address ?? DEFAULT_ADDRESS;
		const port = options.port ?? DEFAULT_PORT;
		const alpn = options.alpn ?? DEFAULT_ALPN;

		const socket = createQuicSocket({
			endpoint: { address, port }
		});
		if (!socket) {
			console.warn('Remote desktop QUIC input unavailable: failed to create socket.');
			return;
		}

		this.attachSessionListener(socket);
		this.attachSocketLifecycleHandlers(socket);

		const listen = (socket as { listen?: (opts: Record<string, unknown>) => Promise<void> }).listen;
		if (typeof listen !== 'function') {
			console.warn('Remote desktop QUIC input unavailable: listen API missing.');
			return;
		}

		try {
			await listen.call(socket, {
				key,
				cert,
				alpn: [alpn]
			});
		} catch (err) {
			this.closeSocketQuietly(socket);
			throw err;
		}

		this.socket = socket;
		this.started = true;
		this.port = port;
		this.alpn = alpn;
		this.address = address;
		console.info(`Remote desktop QUIC input listening on ${address}:${port} (${alpn}).`);
	}

	private configureTokens(options: RemoteDesktopQuicInputOptions) {
		let tokens: string[] = [];
		if (Array.isArray(options.tokens) && options.tokens.length > 0) {
			tokens = parseQuicTokenConfiguration(options.tokens);
		} else {
			tokens = parseQuicTokenConfiguration(process.env.TENVY_QUIC_INPUT_TOKENS);
		}

		this.tokensConfigured = tokens.length > 0;
		this.tokenMatcher = createQuicTokenMatcher(tokens);
	}

	private attachSocketLifecycleHandlers(socket: QuicSocket) {
		const on = (socket as { on?: (event: string, handler: (...args: unknown[]) => void) => void })
			.on;
		if (typeof on !== 'function') {
			return;
		}

		on.call(socket, 'close', () => {
			if (this.socket === socket) {
				this.socket = null;
				this.started = false;
			}
			this.connections.clear();
			this.streamLookup = new WeakMap();
		});

		on.call(socket, 'error', (err: unknown) => {
			console.warn('Remote desktop QUIC input socket error:', err);
			if (this.socket === socket) {
				this.socket = null;
				this.started = false;
			}
			this.connections.clear();
			this.streamLookup = new WeakMap();
		});
	}

	private closeSocketQuietly(socket: QuicSocket) {
		const close = (socket as { close?: () => void }).close;
		if (typeof close === 'function') {
			try {
				close.call(socket);
			} catch (err) {
				console.warn('Failed to close QUIC socket after error:', err);
			}
		}
	}

	private attachSessionListener(socket: QuicSocket) {
		const on = (socket as { on?: (event: string, handler: (...args: unknown[]) => void) => void })
			.on;
		if (typeof on !== 'function') {
			return;
		}

		on.call(socket, 'session', (session: unknown) => {
			this.attachStreamListener(session as QuicSession);
		});
	}

	private closeStreamQuietly(stream: QuicStream) {
		const close = (stream as { close?: () => void }).close;
		if (typeof close === 'function') {
			try {
				close.call(stream);
			} catch (err) {
				console.warn('Failed to close QUIC stream after error:', err);
			}
		}
	}

	private attachStreamListener(session: QuicSession) {
		const on = (session as { on?: (event: string, handler: (...args: unknown[]) => void) => void })
			.on;
		if (typeof on !== 'function') {
			return;
		}

		on.call(session, 'stream', (stream: unknown) => {
			this.consumeStream(session, stream as QuicStream);
		});
	}

	private consumeStream(session: QuicSession, stream: QuicStream) {
		if (!stream) {
			return;
		}

		const setEncoding = (stream as { setEncoding?: (encoding: string) => void }).setEncoding;
		if (typeof setEncoding === 'function') {
			setEncoding.call(stream, 'utf8');
		}

		let buffer = '';
		const handleChunk = (chunk: unknown) => {
			if (typeof chunk !== 'string') {
				return;
			}
			buffer += chunk;
			buffer = this.processBuffer(session, stream, buffer);
		};

		const on = (stream as { on?: (event: string, handler: (...args: unknown[]) => void) => void })
			.on;
		if (typeof on !== 'function') {
			return;
		}

		on.call(stream, 'data', handleChunk);
		on.call(stream, 'close', () => {
			buffer = '';
			this.detachConnectionByStream(stream, 'closed');
		});
		on.call(stream, 'end', () => {
			buffer = '';
			this.detachConnectionByStream(stream, 'ended');
		});
		on.call(stream, 'error', (err: unknown) => {
			console.warn('Remote desktop QUIC input stream error:', err);
			this.detachConnectionByStream(stream, 'error');
		});
	}

	private processBuffer(session: QuicSession, stream: QuicStream, buffer: string) {
		let remaining = buffer;
		while (true) {
			const index = remaining.indexOf('\n');
			if (index === -1) {
				break;
			}
			const raw = remaining.slice(0, index).trim();
			remaining = remaining.slice(index + 1);
			if (raw.length === 0) {
				continue;
			}
			this.handleMessage(session, stream, raw).catch((err) => {
				console.warn('Failed to handle QUIC input packet:', err);
			});
		}

		if (remaining.length > MAX_INPUT_BUFFER) {
			console.warn('Remote desktop QUIC input buffer exceeded limit, discarding partial payload.');
			return '';
		}

		return remaining;
	}

	private async handleMessage(session: QuicSession, stream: QuicStream, raw: string) {
		let packet: RemoteDesktopQuicAgentMessage;
		try {
			packet = JSON.parse(raw) as RemoteDesktopQuicAgentMessage;
		} catch (err) {
			console.warn('Invalid QUIC input payload received:', err);
			return;
		}

		this.handleAgentPacket(session, stream, packet);
	}

	private handleAgentPacket(
		session: QuicSession,
		stream: QuicStream,
		packet: RemoteDesktopQuicAgentMessage
	) {
		const type = typeof packet.type === 'string' ? packet.type.trim().toLowerCase() : '';
		if (!type) {
			return;
		}

		switch (type) {
			case 'register': {
				const agentId = typeof packet.agentId === 'string' ? packet.agentId.trim() : '';
				const sessionId = typeof packet.sessionId === 'string' ? packet.sessionId.trim() : '';
				if (!agentId || !sessionId) {
					return;
				}
				const token = typeof packet.token === 'string' ? packet.token : '';
				this.registerConnection(agentId, sessionId, token, session, stream);
				break;
			}
			case 'ack': {
				break;
			}
			case 'ping': {
				this.sendMessage(stream, {
					type: 'pong',
					timestamp: Date.now()
				});
				break;
			}
			case 'close':
			case 'goodbye': {
				this.detachConnectionByStream(stream, 'agent-close');
				break;
			}
			default:
				break;
		}
	}

	private registerConnection(
		agentId: string,
		sessionId: string,
		token: string,
		session: QuicSession,
		stream: QuicStream
	) {
		if (!this.tokenMatcher(token)) {
			if (this.tokensConfigured) {
				console.warn(
					`Remote desktop QUIC registration rejected for agent ${agentId}: invalid token.`
				);
			}
			this.sendMessage(stream, { type: 'close', reason: 'unauthorized' });
			this.closeStreamQuietly(stream);
			return;
		}

		const existing = this.connections.get(agentId);
		if (existing) {
			if (existing.stream !== stream) {
				this.sendMessage(existing.stream, { type: 'close', reason: 'replaced' });
				this.closeStreamQuietly(existing.stream);
			}
			this.connections.delete(agentId);
		}

		const connection: QuicAgentConnection = { agentId, sessionId, session, stream };
		this.connections.set(agentId, connection);
		this.streamLookup.set(stream as object, connection);
		this.sendMessage(stream, { type: 'registered', sessionId });
	}

	private detachConnectionByStream(stream: QuicStream, reason: string) {
		const connection = this.streamLookup.get(stream as object);
		if (!connection) {
			return;
		}
		this.detachConnection(connection, reason);
	}

	private detachConnection(connection: QuicAgentConnection, reason: string) {
		const current = this.connections.get(connection.agentId);
		if (current && current.stream === connection.stream) {
			this.connections.delete(connection.agentId);
		}
		this.streamLookup.delete(connection.stream as object);
		this.sendMessage(connection.stream, { type: 'close', reason });
		this.closeStreamQuietly(connection.stream);
	}

	private sendMessage(stream: QuicStream, payload: unknown): boolean {
		const write = (stream as { write?: (chunk: string) => unknown }).write;
		if (typeof write !== 'function') {
			return false;
		}

		let serialized = '';
		try {
			serialized = JSON.stringify(payload);
		} catch (err) {
			console.warn('Failed to serialize QUIC message payload:', err);
			return false;
		}

		if (!serialized) {
			return false;
		}

		try {
			const result = write.call(stream, `${serialized}\n`);
			return result !== false;
		} catch (err) {
			console.warn('Remote desktop QUIC input stream write failed:', err);
			return false;
		}
	}

	private async resolveCredential(source: string): Promise<string | null> {
		if (source.includes('-----BEGIN')) {
			return source;
		}
		try {
			const resolved = path.resolve(source);
			return await readFile(resolved, 'utf-8');
		} catch {
			return source;
		}
	}
}

export const remoteDesktopInputService = new RemoteDesktopQuicInputService();

if (process.env.TENVY_QUIC_INPUT_AUTOSTART !== '0') {
	void remoteDesktopInputService.start();
}
