import { decode as decodeMsgpack } from '@msgpack/msgpack';
import type {
	RemoteDesktopDeltaRect,
	RemoteDesktopEncoder,
	RemoteDesktopFramePacket,
	RemoteDesktopMediaSample,
	RemoteDesktopTransport,
	RemoteDesktopTransportDiagnostics,
	RemoteDesktopVideoClip,
	RemoteDesktopVideoFrame,
	RemoteDesktopWebRTCICEServer
} from '$lib/types/remote-desktop';

type DataHandler = (
	payload: RemoteDesktopMediaSample[] | RemoteDesktopFramePacket | string
) => void;

interface WebRTCPipelineOptions {
	offer: string;
	dataChannel?: string;
	iceServers?: RemoteDesktopWebRTCICEServer[];
	gatherTimeoutMs?: number;
	onMessage?: DataHandler;
	label?: string;
	onClose?: () => void;
}

interface WebRTCPipelineResult {
	pipeline: WebRTCPipeline;
	answer: string;
	iceServers: RemoteDesktopWebRTCICEServer[];
}

type StatsLike = Map<string, unknown>;

const encoder = new TextDecoder();

export class WebRTCPipeline {
	private pc: RTCPeerConnection;
	private channel: RTCDataChannel | null = null;
	private closed = false;
	private diagnostics: RemoteDesktopTransportDiagnostics | undefined;
	private messageHandler: DataHandler | undefined;
	private readonly transport: RemoteDesktopTransport = 'webrtc';
	private codec: RemoteDesktopEncoder | undefined;
	private closeCallback: (() => void) | undefined;

	private constructor(pc: RTCPeerConnection) {
		this.pc = pc;
	}

	static async create(options: WebRTCPipelineOptions): Promise<WebRTCPipelineResult> {
		const { RTCPeerConnection } = (await import('@koush/wrtc')) as typeof import('@koush/wrtc');
		const iceServers = normalizeIceServers(options.iceServers ?? []);
		const configuration: RTCConfiguration = { iceServers: toRtcIceServers(iceServers) };
		const pipeline = new WebRTCPipeline(new RTCPeerConnection(configuration));
		pipeline.messageHandler = options.onMessage;
		pipeline.closeCallback = options.onClose;

		const label = options.dataChannel ?? options.label ?? 'remote-desktop-frames';
		pipeline.pc.ondatachannel = (event) => {
			const channel = event.channel;
			if (channel.label !== label) {
				channel.close();
				return;
			}
			pipeline.attachChannel(channel);
		};

		await pipeline.pc.setRemoteDescription({ type: 'offer', sdp: decodeBase64(options.offer) });
		const answer = await pipeline.pc.createAnswer();
		await pipeline.pc.setLocalDescription(answer);

		await waitForIceGathering(pipeline.pc, options.gatherTimeoutMs ?? 15_000);

		const local = pipeline.pc.localDescription;
		if (!local?.sdp) {
			pipeline.close();
			throw new Error('Failed to finalize WebRTC answer');
		}

		pipeline.pc.onconnectionstatechange = () => {
			if (pipeline.pc.connectionState === 'failed' || pipeline.pc.connectionState === 'closed') {
				pipeline.close();
			}
		};

		return {
			pipeline,
			answer: encodeBase64(local.sdp),
			iceServers
		} satisfies WebRTCPipelineResult;
	}

	attachChannel(channel: RTCDataChannel) {
		if (this.closed) {
			try {
				channel.close();
			} catch {
				// ignore
			}
			return;
		}

		this.channel = channel;
		this.channel.binaryType = 'arraybuffer';

		this.channel.onmessage = (event: MessageEvent) => {
			if (this.closed) {
				return;
			}
			const payload = this.decodePayload(event.data);
			if (!payload) {
				return;
			}
			if (Array.isArray(payload)) {
				this.codec = payload.find((sample) => sample.kind === 'video')?.codec as
					| RemoteDesktopEncoder
					| undefined;
			}
			try {
				this.messageHandler?.(payload);
			} catch (err) {
				console.error('WebRTC pipeline message handler failed', err);
			}
		};

		this.channel.onclose = () => {
			this.close();
		};
	}

	send(data: string | ArrayBuffer | ArrayBufferView): boolean {
		if (this.closed || !this.channel || this.channel.readyState !== 'open') {
			return false;
		}
		try {
			this.channel.send(data as string); // types are tricky with wrtc
			return true;
		} catch (err) {
			console.warn('Failed to send data over WebRTC data channel', err);
			return false;
		}
	}

	sendBinary(data: Uint8Array): boolean {
		if (this.closed || !this.channel || this.channel.readyState !== 'open') {
			return false;
		}
		try {
			this.channel.send(data as any);
			return true;
		} catch (err) {
			console.warn('Failed to send binary data over WebRTC data channel', err);
			return false;
		}
	}

	private decodePayload(
		data: unknown
	): RemoteDesktopMediaSample[] | RemoteDesktopFramePacket | string | null {
		try {
			if (typeof data === 'string') {
				return parseStructuredPayload(data);
			}
			const bytes = toUint8Array(data);
			if (bytes) {
				const decoded = tryDecodeBinaryPayload(bytes);
				if (decoded) {
					return decoded;
				}
				return parseStructuredPayload(encoder.decode(bytes));
			}
		} catch (err) {
			console.warn('Failed to decode WebRTC media payload', err);
		}
		return null;
	}

	async collectDiagnostics(): Promise<RemoteDesktopTransportDiagnostics | undefined> {
		if (this.closed || !this.pc) {
			return this.diagnostics;
		}

		try {
			const report = (await this.pc.getStats()) as unknown as StatsLike;
			const diagnostics = extractDiagnostics(report, this.transport, this.codec);
			this.diagnostics = diagnostics ?? this.diagnostics;
		} catch (err) {
			console.warn('Failed to collect WebRTC transport diagnostics', err);
		}

		return this.diagnostics;
	}

	getDiagnostics(): RemoteDesktopTransportDiagnostics | undefined {
		return this.diagnostics;
	}

	close() {
		if (this.closed) {
			return;
		}
		this.closed = true;
		try {
			this.channel?.close();
		} catch {
			// ignore
		}
		try {
			this.pc.close();
		} catch {
			// ignore
		}
		try {
			this.closeCallback?.();
		} catch (err) {
			console.warn('WebRTC pipeline close handler failed', err);
		}
	}
}

function toUint8Array(data: unknown): Uint8Array | null {
	if (data instanceof Uint8Array) {
		return data;
	}
	if (data instanceof ArrayBuffer) {
		return new Uint8Array(data);
	}
	if (ArrayBuffer.isView(data)) {
		const view = data as ArrayBufferView;
		return new Uint8Array(view.buffer, view.byteOffset, view.byteLength);
	}
	return null;
}

function tryDecodeBinaryPayload(
	bytes: Uint8Array
): RemoteDesktopMediaSample[] | RemoteDesktopFramePacket | string | null {
	try {
		const decoded = decodeMsgpack(bytes);
		return normalizeDecodedPayload(decoded);
	} catch {
		return null;
	}
}

function parseStructuredPayload(
	raw: string
): RemoteDesktopMediaSample[] | RemoteDesktopFramePacket | string | null {
	const trimmed = raw.trim();
	if (!trimmed) {
		return null;
	}
	try {
		const parsed = JSON.parse(trimmed) as unknown;
		const normalized = normalizeDecodedPayload(parsed);
		if (normalized) {
			return normalized;
		}
		return trimmed;
	} catch (err) {
		console.warn('Failed to parse WebRTC pipeline payload', err);
		return null;
	}
}

function normalizeDecodedPayload(
	value: unknown
): RemoteDesktopMediaSample[] | RemoteDesktopFramePacket | string | null {
	if (Array.isArray(value)) {
		const media = normalizeMediaSamples(value);
		if (media) {
			return media;
		}
	}
	if (value && typeof value === 'object') {
		const record = value as Record<string, unknown> & { media?: unknown };
		if (Array.isArray(record.media)) {
			const media = normalizeMediaSamples(record.media);
			if (media) {
				return media;
			}
		}
		const frame = normalizeFramePacket(record);
		if (frame) {
			return frame;
		}
	}
	if (typeof value === 'string') {
		return value;
	}
	return null;
}

function normalizeFramePacket(value: Record<string, unknown>): RemoteDesktopFramePacket | null {
	const sessionId = value.sessionId;
	const width = value.width;
	const height = value.height;
	const encoding = value.encoding;
	const timestamp = value.timestamp;
	if (
		typeof sessionId !== 'string' ||
		typeof width !== 'number' ||
		typeof height !== 'number' ||
		typeof encoding !== 'string' ||
		typeof timestamp !== 'string'
	) {
		return null;
	}

	const candidate = value as unknown as RemoteDesktopFramePacket;
	const frame: RemoteDesktopFramePacket = { ...candidate };

	const image = ensureBinaryOrBase64(value.image);
	if (image === null) {
		return null;
	}
	if (image !== undefined) {
		frame.image = image;
	}

	if (Array.isArray(value.deltas)) {
		const deltas: RemoteDesktopFramePacket['deltas'] = [];
		for (const entry of value.deltas as unknown[]) {
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

	if (value.clip && typeof value.clip === 'object') {
		const clipSource = value.clip as { durationMs?: unknown; frames?: unknown };
		const framesSource = Array.isArray(clipSource.frames) ? clipSource.frames : [];
		const frames: RemoteDesktopVideoClip['frames'] = [];
		for (const entry of framesSource) {
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
		frame.clip = {
			durationMs:
				typeof clipSource.durationMs === 'number' && Number.isFinite(clipSource.durationMs)
					? clipSource.durationMs
					: 0,
			frames
		} satisfies RemoteDesktopVideoClip;
	}

	if (Array.isArray(value.media)) {
		const media = normalizeMediaSamples(value.media);
		if (!media) {
			return null;
		}
		frame.media = media;
	}

	return frame;
}

function normalizeMediaSamples(value: unknown): RemoteDesktopMediaSample[] | null {
	if (!Array.isArray(value)) {
		return null;
	}
	const normalized: RemoteDesktopMediaSample[] = [];
	for (const entry of value) {
		if (!entry || typeof entry !== 'object') {
			return null;
		}
		const sample = {
			...(entry as RemoteDesktopMediaSample)
		};
		const data = ensureBinaryOrBase64((entry as { data?: unknown }).data);
		if (data === null || data === undefined) {
			return null;
		}
		sample.data = data;
		normalized.push(sample);
	}
	return normalized;
}

function ensureBinaryOrBase64(value: unknown): string | Uint8Array | undefined | null {
	if (value === undefined) {
		return undefined;
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

function normalizeIceServers(
	servers?: readonly (RemoteDesktopWebRTCICEServer | Record<string, unknown>)[] | null
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

		const entry: RemoteDesktopWebRTCICEServer = { urls: [...cleaned] };

		const username = (server as { username?: unknown }).username;
		if (typeof username === 'string' && username.trim() !== '') {
			entry.username = username.trim();
		}

		const credential = (server as { credential?: unknown }).credential;
		if (typeof credential === 'string' && credential.trim() !== '') {
			entry.credential = credential.trim();
		}

		const credentialType = (server as { credentialType?: unknown }).credentialType;
		if (typeof credentialType === 'string') {
			const normalizedType = credentialType.trim().toLowerCase();
			if (normalizedType === 'oauth') {
				entry.credentialType = 'oauth';
			} else if (normalizedType === 'password' || entry.credential) {
				if (entry.credential) {
					entry.credentialType = 'password';
				}
			}
		} else if (entry.credential) {
			entry.credentialType = 'password';
		}

		normalized.push(entry);
	}

	return normalized;
}

function toRtcIceServers(servers: RemoteDesktopWebRTCICEServer[]): RTCIceServer[] {
	return servers.map((server) => {
		const entry: RTCIceServer & { credentialType?: 'oauth' | 'password' } = {
			urls: [...server.urls]
		};
		if (server.username) {
			entry.username = server.username;
		}
		if (server.credential) {
			entry.credential = server.credential;
		}
		if (server.credentialType === 'oauth') {
			entry.credentialType = 'oauth';
		} else if (
			server.credentialType === 'password' ||
			(!server.credentialType && server.credential)
		) {
			entry.credentialType = 'password';
		}
		return entry;
	});
}

function decodeBase64(value: string): string {
	return Buffer.from(value, 'base64').toString('utf8');
}

function encodeBase64(value: string): string {
	return Buffer.from(value, 'utf8').toString('base64');
}

async function waitForIceGathering(pc: RTCPeerConnection, timeoutMs: number) {
	if (pc.iceGatheringState === 'complete') {
		return;
	}

	await new Promise<void>((resolve, reject) => {
		const timer = setTimeout(() => {
			cleanup();
			reject(new Error('WebRTC ICE gathering timeout'));
		}, timeoutMs);

		const cleanup = () => {
			clearTimeout(timer);
			pc.onicegatheringstatechange = null;
		};

		const checkState = () => {
			if (pc.iceGatheringState === 'complete') {
				cleanup();
				resolve();
			}
		};

		pc.onicegatheringstatechange = () => {
			checkState();
		};

		checkState();
	});
}

function extractDiagnostics(
	stats: StatsLike,
	transport: RemoteDesktopTransport,
	codec?: RemoteDesktopEncoder
): RemoteDesktopTransportDiagnostics | undefined {
	if (!stats) {
		return undefined;
	}

	let availableBitrate: number | undefined;
	let currentBitrate: number | undefined;
	let jitter: number | undefined;
	let rtt: number | undefined;
	let lost: number | undefined;
	let framesDropped: number | undefined;

	const now = new Date().toISOString();

	for (const value of stats.values()) {
		const entry = value as { type?: string } & Record<string, unknown>;
		if (!entry || typeof entry.type !== 'string') continue;

		switch (entry.type) {
			case 'outbound-rtp': {
				const outbound = entry as unknown as {
					bitrateMean?: number;
					bytesSent?: number;
					jitter?: number;
					framesDropped?: number;
				};
				if (typeof outbound.bitrateMean === 'number') {
					currentBitrate = Math.max(0, Math.round(outbound.bitrateMean / 1000));
				}
				if (typeof outbound.jitter === 'number') {
					jitter = Math.max(0, Math.round(outbound.jitter * 1000));
				}
				if (typeof outbound.framesDropped === 'number') {
					framesDropped = outbound.framesDropped;
				}
				break;
			}
			case 'candidate-pair': {
				const pair = entry as unknown as {
					currentRoundTripTime?: number;
					availableOutgoingBitrate?: number;
					totalRoundTripTime?: number;
				};
				if (typeof pair.availableOutgoingBitrate === 'number') {
					availableBitrate = Math.max(0, Math.round(pair.availableOutgoingBitrate / 1000));
				}
				if (typeof pair.currentRoundTripTime === 'number') {
					rtt = Math.max(0, Math.round(pair.currentRoundTripTime * 1000));
				}
				break;
			}
			case 'transport': {
				const transportStats = entry as unknown as { packetsLost?: number };
				if (typeof transportStats.packetsLost === 'number') {
					lost = transportStats.packetsLost;
				}
				break;
			}
		}
	}

	return {
		transport,
		codec,
		bandwidthEstimateKbps: availableBitrate,
		availableBitrateKbps: availableBitrate,
		currentBitrateKbps: currentBitrate,
		jitterMs: jitter,
		rttMs: rtt,
		packetsLost: lost,
		framesDropped,
		lastUpdatedAt: now
	} satisfies RemoteDesktopTransportDiagnostics;
}

export type { WebRTCPipelineResult };
