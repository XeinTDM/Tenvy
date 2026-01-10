<script lang="ts">
	import { browser } from '$app/environment';
	import { onMount } from 'svelte';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import { Card, CardContent, CardFooter } from '$lib/components/ui/card/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import {
		Select,
		SelectContent,
		SelectItem,
		SelectTrigger
	} from '$lib/components/ui/select/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import type { Client } from '$lib/data/clients';
	import type {
		RemoteDesktopFramePacket,
		RemoteDesktopMediaSample,
		RemoteDesktopInputEvent,
		RemoteDesktopMonitor,
		RemoteDesktopMouseButton,
		RemoteDesktopSessionState,
		RemoteDesktopSettings,
		RemoteDesktopSettingsPatch,
		RemoteDesktopStreamMediaMessage,
		RemoteDesktopTransport,
		RemoteDesktopHardwarePreference,
		RemoteDesktopTransportDiagnostics,
		RemoteDesktopTransportCapability,
		RemoteDesktopSessionNegotiationRequest,
		RemoteDesktopSessionNegotiationResponse,
		RemoteDesktopWebRTCICEServer,
		RemoteDesktopEncoder
	} from '$lib/types/remote-desktop';
	import SessionMetricsGrid from './remote-desktop/SessionMetricsGrid.svelte';
	import { createInputChannel } from './remote-desktop/input-channel';
	import { encode as encodeMsgpack, decode as decodeMsgpack } from '@msgpack/msgpack';

	const fallbackMonitors = [
		{ id: 0, label: 'Primary', width: 1280, height: 720 }
	] satisfies RemoteDesktopMonitor[];

	const qualityOptions = [
		{ value: 'auto', label: 'Auto' },
		{ value: 'high', label: 'High' },
		{ value: 'medium', label: 'Medium' },
		{ value: 'low', label: 'Low' }
	] satisfies { value: RemoteDesktopSettings['quality']; label: string }[];

	const encoderOptions = [
		{ value: 'auto', label: 'Auto' },
		{ value: 'hevc', label: 'HEVC (H.265)' },
		{ value: 'avc', label: 'AVC (H.264)' },
		{ value: 'jpeg', label: 'JPEG' }
	] satisfies { value: RemoteDesktopSettings['encoder']; label: string }[];

	const transportOptions = [
		{ value: 'webrtc', label: 'WebRTC (low latency)' },
		{ value: 'http', label: 'HTTP fallback' }
	] satisfies { value: RemoteDesktopTransport; label: string }[];

	const hardwareOptions = [
		{ value: 'auto', label: 'Auto' },
		{ value: 'prefer', label: 'Prefer hardware' },
		{ value: 'avoid', label: 'Avoid hardware' }
	] satisfies { value: RemoteDesktopHardwarePreference; label: string }[];

	const MAX_FRAME_QUEUE = 24;
	const WEBRTC_STATS_INTERVAL_MS = 2_000;
	const WEBRTC_DATA_CHANNEL_LABEL = 'remote-desktop-frames';
	const SUPPORTED_CODECS: RemoteDesktopEncoder[] = ['hevc', 'avc', 'jpeg'];
	const supportsImageBitmap = browser && typeof createImageBitmap === 'function';
	const IMAGE_BASE64_PREFIX = {
		png: 'data:image/png;base64,',
		jpeg: 'data:image/jpeg;base64,'
	} as const;
	const REMOTE_DESKTOP_AUDIO_SAMPLE_RATE = 48_000;

	let { client, initialSession = null } = $props<{
		client: Client;
		initialSession?: RemoteDesktopSessionState | null;
	}>();

	let session = $state<RemoteDesktopSessionState | null>(initialSession ?? null);
	let quality = $state<RemoteDesktopSettings['quality']>('auto');
	let encoder = $state<RemoteDesktopSettings['encoder']>('auto');
	let transportPreference = $state<RemoteDesktopTransport>('webrtc');
	let hardwarePreference = $state<RemoteDesktopHardwarePreference>('auto');
	let targetBitrateKbps = $state<number | null>(null);
	let mode = $state<RemoteDesktopSettings['mode']>('video');
	let monitor = $state(0);
	let mouseEnabled = $state(false);
	let keyboardEnabled = $state(false);
	let encoderHardware = $state<string | null>(null);
	let fps = $state<number | null>(null);
	let bandwidth = $state<number | null>(null);
	let streamWidth = $state<number | null>(null);
	let streamHeight = $state<number | null>(null);
	let latencyMs = $state<number | null>(null);
	let transportDiagnostics = $state<RemoteDesktopTransportDiagnostics | null>(null);
	let localTransportDiagnostics = $state<RemoteDesktopTransportDiagnostics | null>(null);
	let isStarting = $state(false);
	let isStopping = $state(false);
	let isUpdating = $state(false);
	let errorMessage = $state<string | null>(null);
	let infoMessage = $state<string | null>(null);
	let monitors = $state<RemoteDesktopMonitor[]>(fallbackMonitors);
	let sessionActive = $state(false);
	let sessionId = $state('');
	let viewportEl: HTMLDivElement | null = null;
	let webrtcVideoEl: HTMLVideoElement | null = null;
	let viewportFocused = $state(false);
	let pointerCaptured = $state(false);
	let activePointerId: number | null = null;
	interface RemoteDesktopInputDispatchResponse {
		accepted?: boolean;
		delivered?: boolean;
		reason?: string | null;
		message?: string | null;
		error?: string | null;
	}

	let lastInputDispatchAlert: string | null = null;

	const parseInputDispatchResponse = (raw: string): RemoteDesktopInputDispatchResponse | null => {
		const trimmed = raw.trim();
		if (!trimmed) {
			return null;
		}
		try {
			return JSON.parse(trimmed) as RemoteDesktopInputDispatchResponse;
		} catch (err) {
			if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
				console.warn('Failed to parse remote desktop input response payload', err);
			}
			return null;
		}
	};

	const extractInputDispatchReason = (
		payload: RemoteDesktopInputDispatchResponse | null
	): string | null => {
		if (!payload) {
			return null;
		}
		const candidates = [payload.reason, payload.message, payload.error];
		for (const candidate of candidates) {
			if (typeof candidate === 'string' && candidate.trim().length > 0) {
				return candidate.trim();
			}
		}
		return null;
	};

	const formatInputDispatchFallback = (
		response: Response,
		raw: string,
		payload: RemoteDesktopInputDispatchResponse | null
	): string | null => {
		if (payload) {
			return null;
		}
		const trimmed = raw.trim();
		if (trimmed && !trimmed.startsWith('{') && !trimmed.startsWith('[')) {
			return trimmed.length > 160 ? `${trimmed.slice(0, 157)}…` : trimmed;
		}
		if (response.status) {
			const statusText = response.statusText?.trim();
			if (statusText) {
				return `Remote desktop input delivery failed (HTTP ${response.status} ${statusText}).`;
			}
			return `Remote desktop input delivery failed (HTTP ${response.status}).`;
		}
		return null;
	};

	const describeInputDispatchFailure = (reason: string | null | undefined): string | null => {
		if (!reason) {
			return null;
		}
		const trimmed = reason.trim();
		if (!trimmed) {
			return null;
		}
		if (trimmed === 'filtered') {
			return 'Remote desktop input blocked: mouse and keyboard control are disabled for this session.';
		}
		return `Remote desktop input degraded: ${trimmed}`;
	};

	const setInputDispatchError = (reason: string | null, fallback?: string | null) => {
		const described = describeInputDispatchFailure(reason);
		const fallbackTrimmed = typeof fallback === 'string' ? fallback.trim() : '';
		let message = described ?? null;
		if (!message) {
			if (fallbackTrimmed) {
				message = fallbackTrimmed.startsWith('Remote desktop input')
					? fallbackTrimmed
					: `Remote desktop input delivery failed: ${fallbackTrimmed}`;
			} else {
				message = 'Remote desktop input delivery failed.';
			}
		}
		lastInputDispatchAlert = message;
		errorMessage = message;
	};

	const clearInputDispatchError = () => {
		if (lastInputDispatchAlert && errorMessage === lastInputDispatchAlert) {
			errorMessage = null;
		}
		lastInputDispatchAlert = null;
	};
	const inputChannel = browser
		? createInputChannel({
				dispatch: async (events) => {
					if (!client || !sessionActive || !sessionId) {
						return false;
					}
					const payload = encodeMsgpack({ sessionId, events });
					const response = await fetch(`/api/agents/${client.id}/remote-desktop/input`, {
						method: 'POST',
						headers: { 'Content-Type': 'application/msgpack' },
						body: payload as BodyInit,
						keepalive: true
					});
					const raw = await response.text();
					const payloadParsed = parseInputDispatchResponse(raw);
					if (!response.ok) {
						const reason = extractInputDispatchReason(payloadParsed);
						const fallback = reason ?? formatInputDispatchFallback(response, raw, payloadParsed);
						setInputDispatchError(reason, fallback);
						console.warn('Remote desktop input dispatch failed', reason ?? raw);
						return false;
					}
					if (!payloadParsed) {
						setInputDispatchError(null, formatInputDispatchFallback(response, raw, null));
						console.warn('Remote desktop input dispatch returned an empty payload');
						return false;
					}
					if (payloadParsed.accepted === false) {
						const reason = extractInputDispatchReason(payloadParsed);
						setInputDispatchError(reason, 'Remote desktop input request was rejected.');
						console.warn('Remote desktop input dispatch rejected events', reason ?? 'rejected');
						return false;
					}
					if (payloadParsed.delivered === false) {
						const reason = extractInputDispatchReason(payloadParsed);
						setInputDispatchError(reason, 'Remote desktop input was not delivered to the agent.');
						console.warn('Remote desktop input events were not delivered', reason ?? 'unknown');
						return false;
					}
					clearInputDispatchError();
					return true;
				},
				onDispatchFailure: () => {
					if (!lastInputDispatchAlert) {
						setInputDispatchError(null, 'Remote desktop input delivery failed.');
					}
				},
				onDispatchError: (error) => {
					const message =
						error instanceof Error && error.message
							? error.message
							: 'Failed to send remote desktop input events';
					setInputDispatchError(null, message);
					console.error('Failed to send remote desktop input events', error);
				}
			})
		: null;

	const captureTimestamp = () => inputChannel?.captureTimestamp() ?? Date.now();
	const pressedKeys = new SvelteSet<number>();
	const pressedKeyMeta = new SvelteMap<number, { key?: string; code?: string }>();
	const pointerButtonMap: Record<number, RemoteDesktopMouseButton> = {
		0: 'left',
		1: 'middle',
		2: 'right'
	};
	let canvasEl: HTMLCanvasElement | null = null;
	let canvasContext: CanvasRenderingContext2D | null = null;
	let eventSource: EventSource | null = null;
	let streamSessionId: string | null = null;
	let frameQueue: RemoteDesktopFramePacket[] = [];
	let processing = $state(false);
	let stopRequested = $state(false);
	let imageBitmapFallbackLogged = $state(false);
	let skipMouseSync = $state(true);
	let skipKeyboardSync = $state(true);
	let audioContext: AudioContext | null = null;
	let audioQueueTime = 0;
	let webrtcPc: RTCPeerConnection | null = null;
	let webrtcSessionId: string | null = null;
	let webrtcNegotiating = false;
	let webrtcNegotiationAbort: AbortController | null = null;
	let webrtcStatsInterval: ReturnType<typeof setInterval> | null = null;
	let webrtcVideoStream: MediaStream | null = null;
	let webrtcAudioStream: MediaStream | null = null;
	let webrtcAudioSource: MediaStreamAudioSourceNode | null = null;
	let webrtcVideoActive = $state(false);
	let webrtcAudioActive = $state(false);
	let webrtcInboundStats: { bytes: number; timestamp: number } | null = null;
	let webrtcIceServers: RTCIceServer[] | null = null;

	function isDocumentVisible() {
		if (!browser) {
			return false;
		}
		return document.visibilityState === 'visible';
	}

	function maybeStartSession() {
		if (!client || isStarting || sessionActive) {
			return;
		}
		if (!isDocumentVisible()) {
			return;
		}
		void startSession();
	}

	function maybeStopSession(options?: { keepalive?: boolean }) {
		if (!sessionActive || isStopping) {
			return;
		}
		void stopSession(options);
	}

	const qualityLabel = (value: string) => {
		const found = qualityOptions.find((item) => item.value === value);
		return found ? found.label : value;
	};

	const transportLabel = (value: RemoteDesktopTransport) => {
		const found = transportOptions.find((item) => item.value === value);
		return found ? found.label : value;
	};

	const hardwareLabel = (value: RemoteDesktopHardwarePreference) => {
		const found = hardwareOptions.find((item) => item.value === value);
		return found ? found.label : value;
	};

	function formatDiagnosticsSummary(diag: RemoteDesktopTransportDiagnostics | null) {
		if (!diag) {
			return '—';
		}
		const parts: string[] = [];
		if (typeof diag.currentBitrateKbps === 'number') {
			parts.push(`${Math.round(diag.currentBitrateKbps)} kbps`);
		}
		if (typeof diag.rttMs === 'number') {
			parts.push(`${Math.round(diag.rttMs)} ms RTT`);
		}
		if (typeof diag.jitterMs === 'number') {
			parts.push(`${Math.round(diag.jitterMs)} ms jitter`);
		}
		if (parts.length === 0) {
			return '—';
		}
		return parts.join(' · ');
	}

	const monitorLabel = (id: number) => {
		const list = monitors;
		const found = list.find((item: RemoteDesktopMonitor) => item.id === id);
		if (!found) {
			return `Monitor ${id + 1}`;
		}
		return `${found.label} · ${found.width}×${found.height}`;
	};

	async function refreshSession() {
		if (!browser || !client) {
			return session;
		}
		try {
			const response = await fetch(`/api/agents/${client.id}/remote-desktop/session`);
			if (!response.ok) {
				return session;
			}
			const payload = (await response.json()) as {
				session?: RemoteDesktopSessionState | null;
			};
			session = payload.session ?? null;
			const nextSession = payload.session ?? null;
			session = nextSession;
			return nextSession;
		} catch (err) {
			console.warn('Failed to refresh remote desktop session state', err);
			return session;
		}
	}

	const clamp = (value: number, min: number, max: number) => {
		if (Number.isNaN(value)) return min;
		if (value < min) return min;
		if (value > max) return max;
		return value;
	};

	function resetMetrics() {
		fps = null;
		bandwidth = null;
		streamWidth = null;
		streamHeight = null;
		latencyMs = null;
		transportDiagnostics = null;
	}

	function cleanupAudio() {
		if (webrtcAudioActive) {
			return;
		}
		if (audioContext) {
			audioContext.close().catch(() => {});
			audioContext = null;
		}
		audioQueueTime = 0;
	}

	function disconnectStream() {
		if (eventSource) {
			eventSource.close();
			eventSource = null;
		}
		streamSessionId = null;
		stopRequested = true;
		frameQueue = [];
		processing = false;
		imageBitmapFallbackLogged = false;
		inputChannel?.clear();
		cleanupAudio();
	}

	function isWebRTCSupported() {
		return browser && typeof RTCPeerConnection === 'function';
	}

	function encodeBase64(value: string): string {
		const encoder = new TextEncoder();
		const bytes = encoder.encode(value);
		let binary = '';
		for (const byte of bytes) {
			binary += String.fromCharCode(byte);
		}
		return btoa(binary);
	}

	function decodeBase64(value: string): string {
		const binary = atob(value);
		const bytes = new Uint8Array(binary.length);
		for (let index = 0; index < binary.length; index += 1) {
			bytes[index] = binary.charCodeAt(index);
		}
		const decoder = new TextDecoder();
		return decoder.decode(bytes);
	}

	function toRtcIceServers(servers: RemoteDesktopWebRTCICEServer[] | undefined) {
		if (!servers || servers.length === 0) {
			return null;
		}
		const converted: RTCIceServer[] = [];
		for (const server of servers) {
			if (!server || !Array.isArray(server.urls) || server.urls.length === 0) {
				continue;
			}
			const entry: RTCIceServer & {
				credentialType?: 'oauth' | 'password';
			} = {
				urls: [...server.urls]
			};
			if (server.username) {
				entry.username = server.username;
			}
			if (server.credential) {
				entry.credential = server.credential;
			}
			if (server.credentialType === 'oauth' || server.credentialType === 'password') {
				entry.credentialType = server.credentialType;
			}
			converted.push(entry);
		}
		return converted.length > 0 ? converted : null;
	}

	async function waitForPeerIceGathering(pc: RTCPeerConnection): Promise<void> {
		if (pc.iceGatheringState === 'complete') {
			return;
		}
		await new Promise<void>((resolve, reject) => {
			const timeout = setTimeout(() => {
				cleanup();
				reject(new Error('WebRTC ICE gathering timeout'));
			}, 15_000);
			const cleanup = () => {
				clearTimeout(timeout);
				pc.onicegatheringstatechange = null;
			};
			const check = () => {
				if (pc.iceGatheringState === 'complete') {
					cleanup();
					resolve();
				}
			};
			pc.onicegatheringstatechange = () => {
				check();
			};
			check();
		});
	}

	function resetLocalDiagnostics() {
		localTransportDiagnostics = null;
	}

	function stopWebRTCStats() {
		if (webrtcStatsInterval) {
			clearInterval(webrtcStatsInterval);
			webrtcStatsInterval = null;
		}
		webrtcInboundStats = null;
	}

	function detachWebRTCAudio() {
		if (webrtcAudioSource) {
			try {
				webrtcAudioSource.disconnect();
			} catch {
				// ignore
			}
			webrtcAudioSource = null;
		}
		if (webrtcAudioStream) {
			for (const track of webrtcAudioStream.getTracks()) {
				try {
					track.stop();
				} catch {
					// ignore
				}
			}
			webrtcAudioStream = null;
		}
		webrtcAudioActive = false;
	}

	function detachWebRTCVideo() {
		if (webrtcVideoStream) {
			for (const track of webrtcVideoStream.getTracks()) {
				try {
					track.stop();
				} catch {
					// ignore
				}
			}
			webrtcVideoStream = null;
		}
		if (webrtcVideoEl) {
			try {
				webrtcVideoEl.pause();
			} catch {
				// ignore
			}
			webrtcVideoEl.srcObject = null;
		}
		webrtcVideoActive = false;
	}

	function teardownWebRTC() {
		webrtcNegotiationAbort?.abort();
		webrtcNegotiationAbort = null;
		webrtcNegotiating = false;
		stopWebRTCStats();
		if (webrtcPc) {
			try {
				webrtcPc.close();
			} catch {
				// ignore
			}
		}
		webrtcPc = null;
		webrtcSessionId = null;
		detachWebRTCVideo();
		detachWebRTCAudio();
		resetLocalDiagnostics();
	}

	async function attachWebRTCAudioTrack(stream: MediaStream) {
		if (!(await ensureAudioPlaybackContext())) {
			return;
		}
		if (!audioContext) {
			return;
		}
		detachWebRTCAudio();
		try {
			const source = audioContext.createMediaStreamSource(stream);
			source.connect(audioContext.destination);
			webrtcAudioStream = stream;
			webrtcAudioSource = source;
			webrtcAudioActive = true;
		} catch (err) {
			console.warn('Failed to attach remote desktop WebRTC audio track', err);
		}
	}

	function attachWebRTCVideoTrack(stream: MediaStream) {
		if (!webrtcVideoEl) {
			return;
		}
		detachWebRTCVideo();
		webrtcVideoStream = stream;
		try {
			webrtcVideoEl.srcObject = stream;
			webrtcVideoEl.muted = true;
			void webrtcVideoEl.play().catch(() => {});
			webrtcVideoActive = true;
		} catch (err) {
			console.warn('Failed to attach remote desktop WebRTC video track', err);
		}
	}

	async function collectPeerDiagnostics() {
		if (!webrtcPc) {
			return;
		}
		try {
			const report = await webrtcPc.getStats();
			let currentBitrateKbps: number | undefined;
			let jitterMs: number | undefined;
			let packetsLost: number | undefined;
			let rttMs: number | undefined;
			report.forEach((entry: unknown) => {
				if (!entry || typeof entry !== 'object') {
					return;
				}
				const stats = entry as { type?: string } & Record<string, unknown>;
				switch (stats.type) {
					case 'inbound-rtp': {
						const kind = (stats as { kind?: string }).kind;
						if (kind !== 'video' && kind !== 'audio') {
							break;
						}
						const bytes = (stats as { bytesReceived?: number }).bytesReceived;
						const timestamp = (stats as { timestamp?: number }).timestamp;
						if (
							typeof bytes === 'number' &&
							typeof timestamp === 'number' &&
							bytes >= 0 &&
							timestamp > 0
						) {
							if (webrtcInboundStats) {
								const deltaBytes = bytes - webrtcInboundStats.bytes;
								const deltaMs = timestamp - webrtcInboundStats.timestamp;
								if (deltaBytes > 0 && deltaMs > 0) {
									currentBitrateKbps = Math.round((deltaBytes * 8) / deltaMs);
									if (currentBitrateKbps < 0) {
										currentBitrateKbps = undefined;
									}
								}
							}
							webrtcInboundStats = { bytes, timestamp };
						}
						const jitter = (stats as { jitter?: number }).jitter;
						if (typeof jitter === 'number') {
							jitterMs = Math.max(0, Math.round(jitter * 1000));
						}
						const lost = (stats as { packetsLost?: number }).packetsLost;
						if (typeof lost === 'number') {
							packetsLost = Math.max(0, Math.round(lost));
						}
						break;
					}
					case 'candidate-pair': {
						const state = (stats as { state?: string }).state;
						const nominated = (stats as { nominated?: boolean }).nominated;
						if (state === 'succeeded' && nominated !== false) {
							const rtt = (stats as { currentRoundTripTime?: number }).currentRoundTripTime;
							if (typeof rtt === 'number') {
								rttMs = Math.max(0, Math.round(rtt * 1000));
							}
						}
						break;
					}
				}
			});
			const diagnostics: RemoteDesktopTransportDiagnostics = {
				transport: 'webrtc',
				codec: session?.negotiatedCodec ?? undefined,
				currentBitrateKbps,
				jitterMs,
				packetsLost,
				rttMs,
				lastUpdatedAt: new Date().toISOString()
			};
			localTransportDiagnostics = diagnostics;
		} catch (err) {
			console.warn('Failed to collect remote desktop WebRTC diagnostics', err);
		}
	}

	function startWebRTCStats() {
		stopWebRTCStats();
		if (!webrtcPc) {
			return;
		}
		const poll = () => {
			if (!webrtcPc) {
				return;
			}
			void collectPeerDiagnostics();
		};
		poll();
		webrtcStatsInterval = setInterval(poll, WEBRTC_STATS_INTERVAL_MS);
	}

	async function negotiateWebRTC(sessionId: string): Promise<boolean> {
		if (!browser || !client || !isWebRTCSupported()) {
			return false;
		}
		if (webrtcNegotiating) {
			return false;
		}
		webrtcNegotiating = true;
		webrtcNegotiationAbort?.abort();
		const abort = new AbortController();
		webrtcNegotiationAbort = abort;
		try {
			const configuration: RTCConfiguration = webrtcIceServers
				? { iceServers: webrtcIceServers }
				: {};
			const pc = new RTCPeerConnection(configuration);
			webrtcPc = pc;
			webrtcSessionId = sessionId;
			webrtcInboundStats = null;
			pc.addTransceiver('video', { direction: 'recvonly' });
			pc.addTransceiver('audio', { direction: 'recvonly' });
			pc.ontrack = (event) => {
				const [firstStream] = event.streams;
				if (event.track.kind === 'video') {
					if (firstStream) {
						attachWebRTCVideoTrack(firstStream);
					} else {
						attachWebRTCVideoTrack(new MediaStream([event.track]));
					}
				} else if (event.track.kind === 'audio') {
					const stream = firstStream ?? new MediaStream([event.track]);
					void attachWebRTCAudioTrack(stream);
				}
			};
			pc.onconnectionstatechange = () => {
				if (!webrtcPc || pc !== webrtcPc) {
					return;
				}
				if (
					pc.connectionState === 'failed' ||
					pc.connectionState === 'disconnected' ||
					pc.connectionState === 'closed'
				) {
					console.warn('Remote desktop WebRTC connection lost, reverting to HTTP stream');
					teardownWebRTC();
				}
			};

			const channel = pc.createDataChannel(WEBRTC_DATA_CHANNEL_LABEL);
			channel.binaryType = 'arraybuffer';
			channel.onmessage = (event) => {
				if (webrtcVideoActive) return;
				try {
					const payload = decodeMsgpack(new Uint8Array(event.data));
					if (
						payload &&
						typeof payload === 'object' &&
						'sessionId' in payload &&
						typeof (payload as { sessionId: unknown }).sessionId === 'string'
					) {
						// Check if it's a media message
						if ('media' in payload && Array.isArray((payload as { media: unknown }).media)) {
							const mediaMsg = payload as RemoteDesktopStreamMediaMessage;
							void handleMediaSamples(mediaMsg.sessionId, mediaMsg.media);
							return;
						}
						// Assume frame packet
						const frame = payload as RemoteDesktopFramePacket;
						enqueueFrame(frame);
						if (frame.media && frame.media.length > 0) {
							void handleMediaSamples(frame.sessionId, frame.media);
						}
					}
				} catch (err) {
					console.warn('Failed to decode WebRTC binary frame', err);
				}
			};

			const offer = await pc.createOffer();
			await pc.setLocalDescription(offer);
			await waitForPeerIceGathering(pc);
			const local = pc.localDescription;
			if (!local?.sdp) {
				throw new Error('Missing WebRTC local description');
			}

			const transportCapabilities: RemoteDesktopTransportCapability[] = [
				{
					transport: 'webrtc',
					codecs: [...SUPPORTED_CODECS]
				},
				{
					transport: 'http',
					codecs: [...SUPPORTED_CODECS]
				}
			];

			const payload = {
				sessionId,
				transports: transportCapabilities,
				codecs: [...SUPPORTED_CODECS],
				intraRefresh: session?.settings.mode === 'video',
				webrtc: {
					offer: encodeBase64(local.sdp),
					dataChannel: WEBRTC_DATA_CHANNEL_LABEL
				}
			} satisfies RemoteDesktopSessionNegotiationRequest;

			const response = await fetch(`/api/agents/${client.id}/remote-desktop/transport`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(payload),
				signal: abort.signal
			});
			if (!response.ok) {
				const message = (await response.text()) || 'Remote desktop transport negotiation failed';
				throw new Error(message);
			}
			const negotiation = (await response.json()) as RemoteDesktopSessionNegotiationResponse;
			if (
				!negotiation.accepted ||
				negotiation.transport !== 'webrtc' ||
				!negotiation.webrtc?.answer
			) {
				const reason = negotiation.reason?.trim();
				throw new Error(reason || 'WebRTC transport unavailable');
			}

			const answerSdp = decodeBase64(negotiation.webrtc.answer);
			await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp });

			const negotiatedIce = toRtcIceServers(negotiation.webrtc.iceServers);
			if (negotiatedIce) {
				webrtcIceServers = negotiatedIce;
				try {
					pc.setConfiguration({ iceServers: negotiatedIce });
				} catch (err) {
					console.warn('Failed to update WebRTC ICE configuration', err);
				}
			}

			startWebRTCStats();
			return true;
		} catch (err) {
			if (!abort.signal.aborted) {
				console.warn('Remote desktop WebRTC negotiation failed', err);
				teardownWebRTC();
				if (!infoMessage) {
					infoMessage = 'WebRTC transport unavailable; using HTTP stream.';
				}
			}
			return false;
		} finally {
			if (webrtcNegotiationAbort === abort) {
				webrtcNegotiationAbort = null;
			}
			webrtcNegotiating = false;
		}
	}

	async function ensureWebRTC(sessionId: string) {
		if (!browser || !sessionActive || transportPreference !== 'webrtc') {
			return;
		}
		if (!isWebRTCSupported()) {
			return;
		}
		if (webrtcSessionId === sessionId && webrtcPc) {
			return;
		}
		await negotiateWebRTC(sessionId);
	}

	function connectStream(id?: string) {
		if (!browser) return;
		const targetId = id ?? null;
		if (eventSource && streamSessionId === targetId) {
			return;
		}

		disconnectStream();
		stopRequested = false;

		const base = new URL(`/api/agents/${client.id}/remote-desktop/stream`, window.location.origin);
		if (targetId) {
			base.searchParams.set('sessionId', targetId);
		}

		eventSource = new EventSource(base.toString());
		streamSessionId = targetId;

		eventSource.addEventListener('session', (event) => {
			const parsed = parseSessionEvent(event as MessageEvent);
			if (parsed) {
				session = parsed;
				if (!parsed.active) {
					disconnectStream();
				}
			}
		});

		eventSource.addEventListener('frame', (event) => {
			if (webrtcVideoActive) {
				return;
			}
			const frame = parseFrameEvent(event as MessageEvent);
			if (frame) {
				enqueueFrame(frame);
				if (frame.media && frame.media.length > 0) {
					void handleMediaSamples(frame.sessionId, frame.media);
				}
			}
		});

		eventSource.addEventListener('media', (event) => {
			const detail = parseMediaEvent(event as MessageEvent);
			if (!detail) {
				return;
			}
			if (streamSessionId && detail.sessionId && detail.sessionId !== streamSessionId) {
				return;
			}
			void handleMediaSamples(detail.sessionId, detail.media);
		});

		eventSource.addEventListener('end', (event) => {
			const reason = parseEndEvent(event as MessageEvent);
			if (session) {
				session = { ...session, active: false };
			}
			infoMessage = reason ?? 'Remote desktop session ended.';
			disconnectStream();
		});

		eventSource.onerror = () => {
			if (!sessionActive) {
				disconnectStream();
			}
		};
	}

	function parseSessionEvent(event: MessageEvent): RemoteDesktopSessionState | null {
		try {
			const data = JSON.parse(event.data) as { session?: RemoteDesktopSessionState };
			return data?.session ?? null;
		} catch (err) {
			console.error('Failed to parse session event', err);
			return null;
		}
	}

	function parseFrameEvent(event: MessageEvent): RemoteDesktopFramePacket | null {
		try {
			const data = JSON.parse(event.data) as { frame?: RemoteDesktopFramePacket };
			return data?.frame ?? null;
		} catch (err) {
			console.error('Failed to parse frame event', err);
			return null;
		}
	}

	function parseMediaEvent(event: MessageEvent): RemoteDesktopStreamMediaMessage | null {
		try {
			const data = JSON.parse(event.data) as RemoteDesktopStreamMediaMessage;
			if (!data || !Array.isArray(data.media)) {
				return null;
			}
			return data;
		} catch (err) {
			console.error('Failed to parse media event', err);
			return null;
		}
	}

	function parseEndEvent(event: MessageEvent): string | null {
		try {
			const data = JSON.parse(event.data) as { reason?: string };
			return data?.reason ?? null;
		} catch {
			return null;
		}
	}

	async function handleMediaSamples(
		sessionKey: string,
		samples: RemoteDesktopMediaSample[]
	): Promise<void> {
		if (!browser || !samples || samples.length === 0) {
			return;
		}
		const currentSessionId = session?.sessionId ?? null;
		if (currentSessionId && sessionKey && sessionKey !== currentSessionId) {
			return;
		}
		for (const sample of samples) {
			if (webrtcAudioActive) {
				continue;
			}
			if (sample.kind === 'audio') {
				await handleAudioSample(sample);
			}
		}
	}

	async function ensureAudioPlaybackContext(): Promise<boolean> {
		if (!browser) {
			return false;
		}
		if (!audioContext) {
			try {
				audioContext = new AudioContext();
				audioQueueTime = audioContext.currentTime;
			} catch (err) {
				console.warn('Remote desktop audio playback unavailable', err);
				return false;
			}
		}
		if (audioContext.state === 'suspended') {
			try {
				await audioContext.resume();
			} catch (err) {
				console.warn('Failed to resume remote desktop audio context', err);
			}
		}
		return true;
	}

	function decodePcmSample(data: string | Uint8Array): Int16Array | null {
		try {
			if (data instanceof Uint8Array) {
				return new Int16Array(data.buffer, data.byteOffset, data.byteLength);
			}
			const binary = atob(data);
			if (binary.length % 2 !== 0) {
				return null;
			}
			const buffer = new ArrayBuffer(binary.length);
			const bytes = new Uint8Array(buffer);
			for (let i = 0; i < binary.length; i += 1) {
				bytes[i] = binary.charCodeAt(i);
			}
			return new Int16Array(buffer);
		} catch (err) {
			console.warn('Failed to decode remote desktop PCM sample', err);
			return null;
		}
	}

	function scheduleAudioPlayback(pcm: Int16Array, channels: number) {
		if (!audioContext) {
			return;
		}
		const normalizedChannels = Math.max(1, Math.min(2, channels));
		const frameCount = Math.floor(pcm.length / normalizedChannels);
		if (frameCount <= 0) {
			return;
		}
		const buffer = audioContext.createBuffer(
			normalizedChannels,
			frameCount,
			REMOTE_DESKTOP_AUDIO_SAMPLE_RATE
		);
		for (let channel = 0; channel < normalizedChannels; channel += 1) {
			const channelData = buffer.getChannelData(channel);
			for (let frame = 0; frame < frameCount; frame += 1) {
				const sampleIndex = frame * normalizedChannels + channel;
				const value = pcm[sampleIndex] / 32768;
				channelData[frame] = Math.max(-1, Math.min(1, value));
			}
		}
		const source = audioContext.createBufferSource();
		source.buffer = buffer;
		source.connect(audioContext.destination);
		const startAt = Math.max(audioContext.currentTime + 0.05, audioQueueTime);
		source.start(startAt);
		audioQueueTime = startAt + buffer.duration;
	}

	async function handleAudioSample(sample: RemoteDesktopMediaSample): Promise<void> {
		if (!browser) {
			return;
		}
		if (sample.format !== 'pcm' && sample.codec !== 'pcm') {
			console.debug('Unsupported remote desktop audio sample codec', sample.codec);
			return;
		}
		if (!(await ensureAudioPlaybackContext())) {
			return;
		}
		const pcm = decodePcmSample(sample.data);
		if (!pcm) {
			console.warn('Received malformed remote desktop audio sample');
			return;
		}
		const channels = pcm.length % 2 === 0 ? 2 : 1;
		scheduleAudioPlayback(pcm, channels);
	}

	function ensureContext(): CanvasRenderingContext2D | null {
		if (!canvasEl) {
			return null;
		}
		if (!canvasContext) {
			const context = canvasEl.getContext('2d');
			if (!context) {
				return null;
			}
			context.imageSmoothingEnabled = false;
			(
				context as CanvasRenderingContext2D & { mozImageSmoothingEnabled?: boolean }
			).mozImageSmoothingEnabled = false;
			(
				context as CanvasRenderingContext2D & { webkitImageSmoothingEnabled?: boolean }
			).webkitImageSmoothingEnabled = false;
			(
				context as CanvasRenderingContext2D & { msImageSmoothingEnabled?: boolean }
			).msImageSmoothingEnabled = false;
			canvasContext = context;
		}
		return canvasContext;
	}

	function enqueueFrame(frame: RemoteDesktopFramePacket) {
		if (frame.keyFrame) {
			frameQueue = [];
		}
		if (webrtcVideoActive) {
			frameQueue = [];
			return;
		}
		frameQueue.push(frame);

		if (frameQueue.length > MAX_FRAME_QUEUE) {
			while (frameQueue.length > MAX_FRAME_QUEUE) {
				if (frameQueue[0]?.keyFrame && frameQueue.length > 1) {
					frameQueue.splice(1, 1);
				} else {
					frameQueue.shift();
				}
			}
		}

		if (!processing) {
			void processQueue();
		}
	}

	async function processQueue() {
		if (processing) return;
		processing = true;
		try {
			while (frameQueue.length > 0) {
				if (stopRequested) {
					frameQueue = [];
					break;
				}
				const next = frameQueue.shift();
				if (!next) {
					break;
				}
				try {
					await applyFrame(next);
					if (next.metrics) {
						const metrics = next.metrics;
						fps = typeof metrics.fps === 'number' ? metrics.fps : fps;
						bandwidth =
							typeof metrics.bandwidthKbps === 'number' ? metrics.bandwidthKbps : bandwidth;
					}
					streamWidth = typeof next.width === 'number' ? next.width : streamWidth;
					streamHeight = typeof next.height === 'number' ? next.height : streamHeight;
					latencyMs = inputChannel?.computeLatency(next.timestamp) ?? null;
					if (typeof next.encoderHardware === 'string' && next.encoderHardware.length > 0) {
						encoderHardware = next.encoderHardware;
					}
					if (next.monitors && next.monitors.length > 0) {
						monitors = next.monitors;
					}
					if (session) {
						session = {
							...session,
							lastSequence: next.sequence,
							lastUpdatedAt: next.timestamp,
							metrics: next.metrics ?? session.metrics,
							monitors: next.monitors && next.monitors.length > 0 ? next.monitors : session.monitors
						};
					}
				} catch (err) {
					console.error('Failed to apply frame', err);
					errorMessage = err instanceof Error ? err.message : 'Failed to render frame';
				}
			}
		} finally {
			processing = false;
		}
	}

	async function applyFrame(frame: RemoteDesktopFramePacket) {
		const context = ensureContext();
		if (!canvasEl || !context) {
			return;
		}

		if (canvasEl.width !== frame.width || canvasEl.height !== frame.height) {
			canvasEl.width = frame.width;
			canvasEl.height = frame.height;
		}

		if (frame.encoding === 'clip') {
			await applyClipFrame(context, frame);
			return;
		}

		if (frame.keyFrame) {
			if (!frame.image) {
				throw new Error('Missing key frame image data');
			}
			const mime = frame.encoding === 'jpeg' ? 'image/jpeg' : 'image/png';
			if (supportsImageBitmap) {
				try {
					const bitmap = await decodeBitmap(frame.image, mime);
					try {
						context.drawImage(bitmap, 0, 0, frame.width, frame.height);
					} finally {
						bitmap.close();
					}
					return;
				} catch (err) {
					logBitmapFallback(err);
				}
			}
			await drawWithImageElement(
				context,
				frame.image,
				0,
				0,
				frame.width,
				frame.height,
				frame.encoding === 'jpeg' ? 'jpeg' : 'png'
			);
			return;
		}

		if (frame.deltas && frame.deltas.length > 0) {
			if (supportsImageBitmap) {
				try {
					const bitmaps = await Promise.all(
						frame.deltas.map(async (rect) => ({
							rect,
							bitmap: await decodeBitmap(
								rect.data,
								rect.encoding === 'jpeg' ? 'image/jpeg' : 'image/png'
							)
						}))
					);
					try {
						for (const { rect, bitmap } of bitmaps) {
							context.drawImage(bitmap, rect.x, rect.y, rect.width, rect.height);
						}
					} finally {
						for (const { bitmap } of bitmaps) {
							bitmap.close();
						}
					}
					return;
				} catch (err) {
					logBitmapFallback(err);
				}
			}

			for (const rect of frame.deltas) {
				await drawWithImageElement(
					context,
					rect.data,
					rect.x,
					rect.y,
					rect.width,
					rect.height,
					rect.encoding === 'jpeg' ? 'jpeg' : 'png'
				);
			}
		}
	}

	async function applyClipFrame(
		context: CanvasRenderingContext2D,
		frame: RemoteDesktopFramePacket
	) {
		const clip = frame.clip;
		if (!clip || !clip.frames || clip.frames.length === 0) {
			throw new Error('Missing clip frame payload');
		}

		const start = performance.now();
		for (const segment of clip.frames) {
			const target = Math.max(0, segment.offsetMs);
			const elapsed = performance.now() - start;
			const delay = target - elapsed;
			if (delay > 1) {
				await new Promise<void>((resolve) => setTimeout(resolve, delay));
			}

			const mime = segment.encoding === 'jpeg' ? 'image/jpeg' : 'image/png';
			if (supportsImageBitmap) {
				try {
					const bitmap = await decodeBitmap(segment.data, mime);
					try {
						context.drawImage(bitmap, 0, 0, frame.width, frame.height);
					} finally {
						bitmap.close();
					}
					continue;
				} catch (err) {
					logBitmapFallback(err);
				}
			}

			await drawWithImageElement(
				context,
				segment.data,
				0,
				0,
				frame.width,
				frame.height,
				segment.encoding === 'jpeg' ? 'jpeg' : 'png'
			);
		}
	}

	async function decodeBitmap(
		data: string | Uint8Array,
		mimeType: 'image/png' | 'image/jpeg'
	): Promise<ImageBitmap> {
		let blob: Blob;
		if (data instanceof Uint8Array) {
			blob = new Blob([data as unknown as BlobPart], { type: mimeType });
		} else {
			const binary = atob(data);
			const length = binary.length;
			const bytes = new Uint8Array(length);
			for (let i = 0; i < length; i += 1) {
				bytes[i] = binary.charCodeAt(i);
			}
			blob = new Blob([bytes], { type: mimeType });
		}
		return await createImageBitmap(blob);
	}

	function drawWithImageElement(
		context: CanvasRenderingContext2D,
		data: string | Uint8Array,
		x: number,
		y: number,
		width: number,
		height: number,
		encoding: 'png' | 'jpeg'
	): Promise<void> {
		return new Promise((resolve, reject) => {
			const image = new Image();
			image.decoding = 'async';
			let objectUrl: string | null = null;

			image.onload = () => {
				try {
					context.drawImage(image, x, y, width, height);
					resolve();
				} catch (err) {
					reject(err);
				} finally {
					if (objectUrl) {
						URL.revokeObjectURL(objectUrl);
					}
				}
			};
			image.onerror = () => {
				if (objectUrl) {
					URL.revokeObjectURL(objectUrl);
				}
				reject(new Error('Failed to decode frame image segment'));
			};

			if (data instanceof Uint8Array) {
				const mime = encoding === 'jpeg' ? 'image/jpeg' : 'image/png';
				const blob = new Blob([data as unknown as BlobPart], { type: mime });
				objectUrl = URL.createObjectURL(blob);
				image.src = objectUrl;
			} else {
				const prefix = encoding === 'jpeg' ? IMAGE_BASE64_PREFIX.jpeg : IMAGE_BASE64_PREFIX.png;
				image.src = `${prefix}${data}`;
			}
		});
	}

	function logBitmapFallback(err: unknown) {
		if (imageBitmapFallbackLogged) {
			return;
		}
		imageBitmapFallbackLogged = true;
		console.warn('ImageBitmap decode failed, falling back to <img> rendering', err);
	}

	async function startSession() {
		if (!client || isStarting) return;
		errorMessage = null;
		infoMessage = null;
		isStarting = true;
		try {
			const payload = {
				quality,
				monitor,
				mode,
				encoder,
				mouse: mouseEnabled,
				keyboard: keyboardEnabled
			} satisfies RemoteDesktopSettingsPatch & { mouse: boolean; keyboard: boolean };
			const response = await fetch(`/api/agents/${client.id}/remote-desktop/session`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(payload)
			});
			if (!response.ok) {
				const message = (await response.text()) || 'Unable to start remote desktop session';
				throw new Error(message);
			}
			const data = (await response.json()) as { session: RemoteDesktopSessionState | null };
			session = data.session ?? null;
			resetMetrics();
			infoMessage = 'Remote desktop session started.';
			if (session?.sessionId) {
				connectStream(session.sessionId);
			}
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'Failed to start remote desktop session';
		} finally {
			isStarting = false;
		}
	}

	async function stopSession(options?: { keepalive?: boolean }) {
		if (!client || isStopping || !session?.sessionId) return;
		const keepalive = options?.keepalive === true;
		errorMessage = null;
		infoMessage = null;
		isStopping = true;
		try {
			const response = await fetch(`/api/agents/${client.id}/remote-desktop/session`, {
				method: 'DELETE',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ sessionId: session.sessionId }),
				keepalive
			});
			if (!response.ok) {
				const message = (await response.text()) || 'Unable to stop remote desktop session';
				throw new Error(message);
			}
			const data = (await response.json()) as { session: RemoteDesktopSessionState | null };
			session = data.session ?? session;
			infoMessage = 'Remote desktop session paused.';
			disconnectStream();
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'Failed to stop remote desktop session';
		} finally {
			isStopping = false;
		}
	}

	async function updateSession(partial: RemoteDesktopSettingsPatch) {
		if (!client || !session?.sessionId) return;
		if (Object.keys(partial).length === 0) {
			return;
		}
		isUpdating = true;
		try {
			const response = await fetch(`/api/agents/${client.id}/remote-desktop/session`, {
				method: 'PATCH',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ sessionId: session.sessionId, ...partial })
			});
			if (!response.ok) {
				const message = (await response.text()) || 'Failed to update session settings';
				throw new Error(message);
			}
			const data = (await response.json()) as { session: RemoteDesktopSessionState | null };
			session = data.session ?? session;
		} catch (err) {
			errorMessage =
				err instanceof Error ? err.message : 'Failed to update remote desktop settings';
		} finally {
			isUpdating = false;
		}
	}

	function handleBitrateInput(event: Event) {
		const element = event.currentTarget as HTMLInputElement;
		const parsed = Number.parseInt(element.value, 10);
		if (Number.isNaN(parsed) || parsed <= 0) {
			targetBitrateKbps = null;
			element.value = '';
			if (sessionActive) {
				void updateSession({ targetBitrateKbps: 0 });
			}
			return;
		}
		targetBitrateKbps = parsed;
		if (sessionActive) {
			void updateSession({ targetBitrateKbps: parsed });
		}
	}

	function queueInput(event: RemoteDesktopInputEvent) {
		if (!browser || !sessionActive || !sessionId || !client || !inputChannel) {
			return;
		}
		inputChannel.enqueue(event);
	}

	function queueInputBatch(events: RemoteDesktopInputEvent[]) {
		if (!browser || !sessionActive || !sessionId || !client || !inputChannel) {
			return;
		}
		if (events.length === 0) {
			return;
		}
		inputChannel.enqueueBatch(events);
	}

	function releasePointerCapture() {
		if (!viewportEl) {
			pointerCaptured = false;
			activePointerId = null;
			return;
		}
		if (!pointerCaptured || activePointerId === null) {
			pointerCaptured = false;
			activePointerId = null;
			return;
		}
		try {
			viewportEl.releasePointerCapture(activePointerId);
		} catch {
			// ignore
		}
		pointerCaptured = false;
		activePointerId = null;
	}

	function pointerButtonFromEvent(button: number): RemoteDesktopMouseButton | null {
		if (button === 0) return 'left';
		if (button === 1) return 'middle';
		if (button === 2) return 'right';
		const mapped = pointerButtonMap[button];
		return mapped ?? null;
	}

	function handlePointerMove(event: PointerEvent) {
		if (!browser || event.pointerType !== 'mouse') {
			return;
		}
		if (!mouseEnabled || !sessionActive) {
			return;
		}
		if (!canvasEl) {
			return;
		}
		const rect = canvasEl.getBoundingClientRect();
		if (rect.width <= 0 || rect.height <= 0) {
			return;
		}
		const x = clamp((event.clientX - rect.left) / rect.width, 0, 1);
		const y = clamp((event.clientY - rect.top) / rect.height, 0, 1);
		queueInput({
			type: 'mouse-move',
			x,
			y,
			normalized: true,
			monitor,
			capturedAt: captureTimestamp()
		});
	}

	function handlePointerDown(event: PointerEvent) {
		if (!browser || event.pointerType !== 'mouse') {
			return;
		}
		if (!mouseEnabled || !sessionActive) {
			return;
		}
		event.preventDefault();
		viewportEl?.focus();
		handlePointerMove(event);
		const button = pointerButtonFromEvent(event.button);
		if (button) {
			queueInput({
				type: 'mouse-button',
				button,
				pressed: true,
				monitor,
				capturedAt: captureTimestamp()
			});
		}
		const target = event.currentTarget as HTMLDivElement | null;
		if (target) {
			try {
				target.setPointerCapture(event.pointerId);
				pointerCaptured = true;
				activePointerId = event.pointerId;
			} catch {
				pointerCaptured = false;
				activePointerId = null;
			}
		}
	}

	function handlePointerUp(event: PointerEvent) {
		if (!browser || event.pointerType !== 'mouse') {
			return;
		}
		if (!mouseEnabled || !sessionActive) {
			releasePointerCapture();
			return;
		}
		event.preventDefault();
		const button = pointerButtonFromEvent(event.button);
		if (button) {
			queueInput({
				type: 'mouse-button',
				button,
				pressed: false,
				monitor,
				capturedAt: captureTimestamp()
			});
		}
		if (pointerCaptured && activePointerId === event.pointerId) {
			releasePointerCapture();
		}
	}

	function handlePointerLeave() {
		if (!pointerCaptured) {
			return;
		}
		releasePointerCapture();
	}

	function handleWheel(event: WheelEvent) {
		if (!mouseEnabled || !sessionActive) {
			return;
		}
		event.preventDefault();
		event.stopPropagation();
		queueInput({
			type: 'mouse-scroll',
			deltaX: event.deltaX,
			deltaY: event.deltaY,
			deltaMode: event.deltaMode,
			monitor,
			capturedAt: captureTimestamp()
		});
	}

	function handleViewportFocus() {
		viewportFocused = true;
	}

	function handleViewportBlur() {
		viewportFocused = false;
		releasePointerCapture();
		releaseAllPressedKeys();
	}

	function keyCodeFromEvent(event: KeyboardEvent) {
		const raw = (event as KeyboardEvent & { which?: number }).keyCode ?? event.which;
		if (typeof raw !== 'number' || Number.isNaN(raw)) {
			return 0;
		}
		return Math.trunc(raw);
	}

	function createKeyEvent(
		pressed: boolean,
		keyCode: number,
		event: KeyboardEvent,
		meta?: { key?: string; code?: string }
	): RemoteDesktopInputEvent {
		return {
			type: 'key',
			pressed,
			keyCode,
			key: event.key ?? meta?.key,
			code: event.code ?? meta?.code,
			repeat: pressed ? event.repeat : false,
			altKey: event.altKey,
			ctrlKey: event.ctrlKey,
			shiftKey: event.shiftKey,
			metaKey: event.metaKey,
			capturedAt: captureTimestamp()
		};
	}

	function handleKeyDown(event: KeyboardEvent) {
		if (!keyboardEnabled || !sessionActive || !viewportFocused) {
			return;
		}
		const keyCode = keyCodeFromEvent(event);
		if (keyCode <= 0) {
			return;
		}
		event.preventDefault();
		if (!event.repeat && !pressedKeys.has(keyCode)) {
			pressedKeys.add(keyCode);
			pressedKeyMeta.set(keyCode, { key: event.key, code: event.code });
		}
		const meta = pressedKeyMeta.get(keyCode);
		queueInput(createKeyEvent(true, keyCode, event, meta));
	}

	function handleKeyUp(event: KeyboardEvent) {
		const keyCode = keyCodeFromEvent(event);
		if (keyCode <= 0) {
			return;
		}
		const meta = pressedKeyMeta.get(keyCode);
		pressedKeys.delete(keyCode);
		pressedKeyMeta.delete(keyCode);
		if (!keyboardEnabled || !sessionActive) {
			return;
		}
		event.preventDefault();
		queueInput(createKeyEvent(false, keyCode, event, meta));
	}

	function releaseAllPressedKeys() {
		if (pressedKeys.size === 0) {
			pressedKeyMeta.clear();
			return;
		}
		const events: RemoteDesktopInputEvent[] = [];
		for (const code of pressedKeys) {
			const meta = pressedKeyMeta.get(code);
			events.push({
				type: 'key',
				pressed: false,
				keyCode: code,
				key: meta?.key,
				code: meta?.code,
				altKey: false,
				ctrlKey: false,
				shiftKey: false,
				metaKey: false,
				capturedAt: captureTimestamp()
			});
		}
		pressedKeys.clear();
		pressedKeyMeta.clear();
		queueInputBatch(events);
	}

	$effect(() => {
		if (!mouseEnabled) {
			releasePointerCapture();
		}
	});

	$effect(() => {
		if (!keyboardEnabled) {
			releaseAllPressedKeys();
		}
	});

	$effect(() => {
		if (!sessionActive) {
			releasePointerCapture();
			releaseAllPressedKeys();
			inputChannel?.clear();
			teardownWebRTC();
		}
	});

	$effect(() => {
		const current = session;
		if (!current) {
			quality = 'auto';
			encoder = 'auto';
			transportPreference = 'webrtc';
			hardwarePreference = 'auto';
			targetBitrateKbps = null;
			encoderHardware = null;
			mode = 'video';
			monitor = 0;
			mouseEnabled = true;
			keyboardEnabled = true;
			sessionActive = false;
			sessionId = '';
			monitors = fallbackMonitors;
			transportDiagnostics = null;
			resetMetrics();
			return;
		}
		quality = current.settings.quality;
		const configuredEncoder = current.settings.encoder ?? 'auto';
		encoder = configuredEncoder;
		encoderHardware = current.encoderHardware ?? encoderHardware;
		mode = current.settings.mode;
		monitor = current.settings.monitor;
		mouseEnabled = current.settings.mouse;
		keyboardEnabled = current.settings.keyboard;
		transportPreference = current.settings.transport ?? 'webrtc';
		hardwarePreference = current.settings.hardware ?? 'auto';
		const bitrate = current.settings.targetBitrateKbps ?? 0;
		targetBitrateKbps = bitrate > 0 ? bitrate : null;
		sessionActive = current.active === true;
		sessionId = current.sessionId ?? '';
		monitors =
			current.monitors && current.monitors.length > 0 ? current.monitors : fallbackMonitors;
		if (current.metrics) {
			fps = typeof current.metrics.fps === 'number' ? current.metrics.fps : fps;
			bandwidth =
				typeof current.metrics.bandwidthKbps === 'number'
					? current.metrics.bandwidthKbps
					: bandwidth;
		}
		if (!localTransportDiagnostics) {
			transportDiagnostics = current.transportDiagnostics ?? null;
		}
	});

	$effect(() => {
		const localDiagnostics = localTransportDiagnostics;
		if (localDiagnostics) {
			transportDiagnostics = localDiagnostics;
			return;
		}
		const sessionDiagnostics = session?.transportDiagnostics ?? null;
		transportDiagnostics = sessionDiagnostics;
	});

	$effect(() => {
		if (!sessionActive) {
			skipMouseSync = true;
			skipKeyboardSync = true;
		}
	});

	$effect(() => {
		if (!sessionActive) {
			return;
		}
		const current = session;
		if (!current) {
			return;
		}
		if (current.settings.mouse === mouseEnabled) {
			skipMouseSync = false;
			return;
		}
		if (skipMouseSync) {
			skipMouseSync = false;
			return;
		}
		void updateSession({ mouse: mouseEnabled });
	});

	$effect(() => {
		if (!sessionActive) {
			return;
		}
		const current = session;
		if (!current) {
			return;
		}
		if (current.settings.keyboard === keyboardEnabled) {
			skipKeyboardSync = false;
			return;
		}
		if (skipKeyboardSync) {
			skipKeyboardSync = false;
			return;
		}
		void updateSession({ keyboard: keyboardEnabled });
	});

	$effect(() => {
		if (!sessionActive || !sessionId) {
			disconnectStream();
			return;
		}
		connectStream(sessionId);
	});

	$effect(() => {
		const active = sessionActive;
		const currentSession = sessionId;
		const negotiated = session?.negotiatedTransport ?? null;
		if (!browser) {
			return;
		}
		if (!active || transportPreference !== 'webrtc') {
			teardownWebRTC();
			return;
		}
		if (!currentSession) {
			return;
		}
		if (negotiated && negotiated !== 'webrtc') {
			teardownWebRTC();
			return;
		}
		void ensureWebRTC(currentSession);
	});

	onMount(() => {
		if (!browser) {
			return () => {
				disconnectStream();
			};
		}

		let destroyed = false;

		const initialize = async () => {
			const currentSession = await refreshSession();
			if (destroyed) {
				return;
			}
			if (currentSession?.active && currentSession.sessionId) {
				connectStream(currentSession.sessionId);
			} else {
				maybeStartSession();
			}
		};

		void initialize();

		const handleVisibilityChange = () => {
			if (document.visibilityState === 'visible') {
				maybeStartSession();
			} else {
				maybeStopSession({ keepalive: true });
			}
		};

		const handlePageHide = () => {
			maybeStopSession({ keepalive: true });
		};

		document.addEventListener('visibilitychange', handleVisibilityChange);
		window.addEventListener('pagehide', handlePageHide);
		window.addEventListener('beforeunload', handlePageHide);

		return () => {
			destroyed = true;
			document.removeEventListener('visibilitychange', handleVisibilityChange);
			window.removeEventListener('pagehide', handlePageHide);
			window.removeEventListener('beforeunload', handlePageHide);
			maybeStopSession({ keepalive: true });
			disconnectStream();
			teardownWebRTC();
		};
	});
</script>

<svelte:window onkeydown={handleKeyDown} onkeyup={handleKeyUp} />

<Card>
	<CardContent>
		<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
		<div
			tabindex={0}
			bind:this={viewportEl}
			class="relative overflow-hidden rounded-lg border border-border bg-muted/30 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
			role="application"
			aria-label="Remote desktop viewport"
			onfocus={handleViewportFocus}
			onblur={handleViewportBlur}
			onpointerdown={handlePointerDown}
			onpointerup={handlePointerUp}
			onpointermove={handlePointerMove}
			onpointerleave={handlePointerLeave}
			onpointercancel={handlePointerLeave}
			onwheel={handleWheel}
			style="touch-action: none;"
		>
			<video
				bind:this={webrtcVideoEl}
				class="absolute inset-0 h-full w-full object-contain transition-opacity duration-150"
				class:opacity-0={!webrtcVideoActive}
				autoplay
				playsinline
				muted
				controls={false}
				style="pointer-events: none; image-rendering: pixelated;"
			></video>
			<canvas
				bind:this={canvasEl}
				class="block h-full w-full bg-slate-950"
				style="image-rendering: pixelated;"
				class:hidden={webrtcVideoActive}
			></canvas>
			{#if !sessionActive}
				<div
					class="absolute inset-0 flex items-center justify-center text-sm text-muted-foreground"
				>
					Session inactive · start streaming to receive frames
				</div>
			{/if}
		</div>
		<SessionMetricsGrid {fps} {bandwidth} {streamWidth} {streamHeight} {latencyMs} />
		<div class="mt-3 grid gap-2 text-xs text-muted-foreground sm:grid-cols-2">
			<div class="space-y-1">
				<p>
					<span class="font-semibold text-foreground">Transport:</span>
					{session?.negotiatedTransport ? transportLabel(session.negotiatedTransport) : '—'}
					{session?.negotiatedCodec ? ` · ${session.negotiatedCodec.toUpperCase()}` : ''}
				</p>
				<p>
					<span class="font-semibold text-foreground">Hardware encoder:</span>
					{encoderHardware ?? '—'} · {hardwareLabel(hardwarePreference)}
				</p>
			</div>
			<div class="space-y-1">
				<p>
					<span class="font-semibold text-foreground">Target bitrate:</span>
					{targetBitrateKbps ? `${targetBitrateKbps} kbps` : 'Auto'}
				</p>
				<p>
					<span class="font-semibold text-foreground">Observed:</span>
					{formatDiagnosticsSummary(transportDiagnostics)}
				</p>
			</div>
		</div>
		{#if errorMessage}
			<p class="text-sm text-destructive">{errorMessage}</p>
		{/if}
		{#if infoMessage}
			<p class="text-sm text-emerald-500">{infoMessage}</p>
		{/if}
	</CardContent>
	<CardFooter
		class="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground"
	>
		<div class="flex flex-wrap gap-4">
			<div class="w-70">
				<Label class="text-sm font-medium" for="quality-select">Quality</Label>
				<Select
					type="single"
					value={quality}
					onValueChange={(value) => {
						quality = value as RemoteDesktopSettings['quality'];
						if (sessionActive) {
							void updateSession({ quality });
						}
					}}
				>
					<SelectTrigger id="quality-select" class="w-full" disabled={isUpdating && sessionActive}>
						<span class="truncate">{qualityLabel(quality)}</span>
					</SelectTrigger>
					<SelectContent>
						{#each qualityOptions as option (option.value)}
							<SelectItem value={option.value}>{option.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<div class="w-70">
				<Label class="text-sm font-medium" for="transport-select">Transport</Label>
				<Select
					type="single"
					value={transportPreference}
					onValueChange={(value) => {
						transportPreference = value as RemoteDesktopTransport;
						if (sessionActive) {
							void updateSession({ transport: transportPreference });
						}
					}}
				>
					<SelectTrigger
						id="transport-select"
						class="w-full"
						disabled={isUpdating && sessionActive}
					>
						<span class="truncate">{transportLabel(transportPreference)}</span>
					</SelectTrigger>
					<SelectContent>
						{#each transportOptions as option (option.value)}
							<SelectItem value={option.value}>{option.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<div class="w-70">
				<Label class="text-sm font-medium" for="encoder-select">Encoder</Label>
				<Select
					type="single"
					value={encoder}
					onValueChange={(value) => {
						encoder = value as RemoteDesktopSettings['encoder'];
						if (sessionActive) {
							void updateSession({ encoder });
						}
					}}
				>
					<SelectTrigger id="encoder-select" class="w-full" disabled={isUpdating && sessionActive}>
						<span class="truncate"
							>{encoderOptions.find((item) => item.value === encoder)?.label ?? encoder}</span
						>
					</SelectTrigger>
					<SelectContent>
						{#each encoderOptions as option (option.value)}
							<SelectItem value={option.value}>{option.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<div class="w-70">
				<Label class="text-sm font-medium" for="hardware-select">Hardware</Label>
				<Select
					type="single"
					value={hardwarePreference}
					onValueChange={(value) => {
						hardwarePreference = value as RemoteDesktopHardwarePreference;
						if (sessionActive) {
							void updateSession({ hardware: hardwarePreference });
						}
					}}
				>
					<SelectTrigger id="hardware-select" class="w-full" disabled={isUpdating && sessionActive}>
						<span class="truncate">{hardwareLabel(hardwarePreference)}</span>
					</SelectTrigger>
					<SelectContent>
						{#each hardwareOptions as option (option.value)}
							<SelectItem value={option.value}>{option.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<div class="w-70">
				<Label class="text-sm font-medium" for="monitor-select">Monitor</Label>
				<Select
					type="single"
					value={monitor.toString()}
					onValueChange={(value) => {
						const next = Number.parseInt(value, 10);
						monitor = Number.isNaN(next) ? 0 : next;
						if (sessionActive) {
							void updateSession({ monitor });
						}
					}}
				>
					<SelectTrigger id="monitor-select" class="w-full" disabled={isUpdating && sessionActive}>
						<span class="truncate">{monitorLabel(monitor)}</span>
					</SelectTrigger>
					<SelectContent>
						{#each monitors as item (item.id)}
							<SelectItem value={item.id.toString()}>
								Monitor {item.id + 1} · {item.width}×{item.height}
							</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<div class="w-56">
				<Label class="text-sm font-medium" for="bitrate-input">Target bitrate (kbps)</Label>
				<Input
					id="bitrate-input"
					type="number"
					min="0"
					step="100"
					placeholder="Auto"
					value={targetBitrateKbps ?? ''}
					disabled={!sessionActive || isUpdating}
					on:input={handleBitrateInput}
				/>
			</div>
			<div class="flex items-center gap-2">
				<p class="text-sm font-medium">Mouse control</p>
				<Switch
					bind:checked={mouseEnabled}
					disabled={!sessionActive || isUpdating}
					aria-label="Toggle mouse control"
				/>
			</div>

			<div class="flex items-center gap-2">
				<p class="text-sm font-medium">Keyboard control</p>
				<Switch
					bind:checked={keyboardEnabled}
					disabled={!sessionActive || isUpdating}
					aria-label="Toggle keyboard control"
				/>
			</div>
		</div>
		<div class="flex gap-2">
			{#if sessionActive}
				<Button variant="destructive" disabled={isStopping} onclick={() => stopSession()}>
					{isStopping ? 'Pausing…' : 'Pause session'}
				</Button>
			{:else}
				<Button disabled={isStarting} onclick={startSession}>
					{isStarting ? 'Starting…' : 'Start session'}
				</Button>
			{/if}
		</div>
	</CardFooter>
</Card>
