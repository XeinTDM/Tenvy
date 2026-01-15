import { browser } from '$app/environment';
import { decode as decodeMsgpack } from '@msgpack/msgpack';
import type {
	RemoteDesktopFramePacket,
	RemoteDesktopMediaSample,
	RemoteDesktopSessionState,
	RemoteDesktopStreamMediaMessage,
	RemoteDesktopTransportDiagnostics,
	RemoteDesktopTransport,
	RemoteDesktopTransportCapability,
	RemoteDesktopSessionNegotiationRequest,
	RemoteDesktopSessionNegotiationResponse,
	RemoteDesktopWebRTCICEServer,
	RemoteDesktopEncoder
} from '$lib/types/remote-desktop';
import { encodeBase64, decodeBase64, toRtcIceServers, waitForPeerIceGathering } from './utils';

export interface TransportControllerOptions {
	agentId: string;
	onFrame: (frame: RemoteDesktopFramePacket) => void;
	onMedia: (sessionId: string, media: RemoteDesktopMediaSample[]) => void;
	onSessionUpdate: (session: RemoteDesktopSessionState) => void;
	onEnd: (reason?: string) => void;
	onError: (message: string) => void;
	onInfo: (message: string) => void;
}

const WEBRTC_DATA_CHANNEL_LABEL = 'remote-desktop-frames';
const WEBRTC_STATS_INTERVAL_MS = 2_000;
const SUPPORTED_CODECS: RemoteDesktopEncoder[] = ['hevc', 'avc', 'jpeg'];

export function createTransportController(options: TransportControllerOptions) {
	let eventSource = $state<EventSource | null>(null);
	let webrtcPc = $state<RTCPeerConnection | null>(null);
	let webrtcNegotiating = $state(false);
	let webrtcVideoActive = $state(false);
	let webrtcAudioActive = $state(false);
	let transportDiagnostics = $state<RemoteDesktopTransportDiagnostics | null>(null);
	
	let webrtcSessionId: string | null = null;
	let webrtcNegotiationAbort: AbortController | null = null;
	let webrtcStatsInterval: any = null;
	let webrtcVideoStream: MediaStream | null = null;
	let webrtcAudioStream: MediaStream | null = null;
	let webrtcAudioSource: MediaStreamAudioSourceNode | null = null;
	let webrtcInboundStats: { bytes: number; timestamp: number } | null = null;
	let webrtcIceServers: RTCIceServer[] | null = null;
	let webrtcVideoEl: HTMLVideoElement | null = null;
	let audioContext: AudioContext | null = null;

	function isWebRTCSupported() {
		return browser && typeof RTCPeerConnection === 'function';
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
			try { webrtcAudioSource.disconnect(); } catch { }
			webrtcAudioSource = null;
		}
		if (webrtcAudioStream) {
			for (const track of webrtcAudioStream.getTracks()) {
				try { track.stop(); } catch { }
			}
			webrtcAudioStream = null;
		}
		webrtcAudioActive = false;
	}

	function detachWebRTCVideo() {
		if (webrtcVideoStream) {
			for (const track of webrtcVideoStream.getTracks()) {
				try { track.stop(); } catch { }
			}
			webrtcVideoStream = null;
		}
		if (webrtcVideoEl) {
			try { webrtcVideoEl.pause(); } catch { }
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
			try { webrtcPc.close(); } catch { }
		}
		webrtcPc = null;
		webrtcSessionId = null;
		detachWebRTCVideo();
		detachWebRTCAudio();
		transportDiagnostics = null;
	}

	async function collectPeerDiagnostics(negotiatedCodec?: RemoteDesktopEncoder) {
		if (!webrtcPc) return;
		try {
			const report = await webrtcPc.getStats();
			let currentBitrateKbps: number | undefined;
			let jitterMs: number | undefined;
			let packetsLost: number | undefined;
			let rttMs: number | undefined;
			
			report.forEach((entry: any) => {
				if (!entry || typeof entry !== 'object') return;
				switch (entry.type) {
					case 'inbound-rtp': {
						if (entry.kind !== 'video' && entry.kind !== 'audio') break;
						const bytes = entry.bytesReceived;
						const timestamp = entry.timestamp;
						if (typeof bytes === 'number' && typeof timestamp === 'number' && bytes >= 0 && timestamp > 0) {
							if (webrtcInboundStats) {
								const deltaBytes = bytes - webrtcInboundStats.bytes;
								const deltaMs = timestamp - webrtcInboundStats.timestamp;
								if (deltaBytes > 0 && deltaMs > 0) {
									currentBitrateKbps = Math.round((deltaBytes * 8) / deltaMs);
								}
							}
							webrtcInboundStats = { bytes, timestamp };
						}
						if (typeof entry.jitter === 'number') {
							jitterMs = Math.max(0, Math.round(entry.jitter * 1000));
						}
						if (typeof entry.packetsLost === 'number') {
							packetsLost = Math.max(0, Math.round(entry.packetsLost));
						}
						break;
					}
					case 'candidate-pair': {
						if (entry.state === 'succeeded' && entry.nominated !== false) {
							if (typeof entry.currentRoundTripTime === 'number') {
								rttMs = Math.max(0, Math.round(entry.currentRoundTripTime * 1000));
							}
						}
						break;
					}
				}
			});
			
			transportDiagnostics = {
				transport: 'webrtc',
				codec: negotiatedCodec,
				currentBitrateKbps,
				jitterMs,
				packetsLost,
				rttMs,
				lastUpdatedAt: new Date().toISOString()
			};
		} catch (err) {
			console.warn('Failed to collect remote desktop WebRTC diagnostics', err);
		}
	}

	function startWebRTCStats(negotiatedCodec?: RemoteDesktopEncoder) {
		stopWebRTCStats();
		if (!webrtcPc) return;
		webrtcStatsInterval = setInterval(() => collectPeerDiagnostics(negotiatedCodec), WEBRTC_STATS_INTERVAL_MS);
	}

	async function negotiateWebRTC(sessionId: string, settings: { mode: string }, iceServers: RTCIceServer[] | null): Promise<boolean> {
		if (!browser || !isWebRTCSupported() || webrtcNegotiating) return false;
		
		webrtcNegotiating = true;
		webrtcNegotiationAbort?.abort();
		const abort = new AbortController();
		webrtcNegotiationAbort = abort;

		try {
			const configuration: RTCConfiguration = iceServers ? { iceServers } : {};
			const pc = new RTCPeerConnection(configuration);
			webrtcPc = pc;
			webrtcSessionId = sessionId;

			pc.addTransceiver('video', { direction: 'recvonly' });
			pc.addTransceiver('audio', { direction: 'recvonly' });

			pc.ontrack = (event) => {
				const [firstStream] = event.streams;
				const stream = firstStream ?? new MediaStream([event.track]);
				if (event.track.kind === 'video') {
					webrtcVideoStream = stream;
					if (webrtcVideoEl) {
						webrtcVideoEl.srcObject = stream;
						webrtcVideoEl.muted = true;
						webrtcVideoEl.play().catch(() => {});
					}
					webrtcVideoActive = true;
				} else if (event.track.kind === 'audio') {
					webrtcAudioStream = stream;
					webrtcAudioActive = true;
				}
			};

			pc.onconnectionstatechange = () => {
				if (pc.connectionState === 'failed' || pc.connectionState === 'disconnected' || pc.connectionState === 'closed') {
					teardownWebRTC();
				}
			};

			const channel = pc.createDataChannel(WEBRTC_DATA_CHANNEL_LABEL);
			channel.binaryType = 'arraybuffer';
			channel.onmessage = (event) => {
				if (webrtcVideoActive) return;
				try {
					const payload = decodeMsgpack(new Uint8Array(event.data)) as any;
					if (payload?.sessionId) {
						if (payload.media) {
							options.onMedia(payload.sessionId, payload.media);
						} else {
							options.onFrame(payload as RemoteDesktopFramePacket);
							if (payload.media) options.onMedia(payload.sessionId, payload.media);
						}
					}
				} catch (err) {
					console.warn('Failed to decode WebRTC binary frame', err);
				}
			};

			const offer = await pc.createOffer();
			await pc.setLocalDescription(offer);
			await waitForPeerIceGathering(pc);
			
			const payload: RemoteDesktopSessionNegotiationRequest = {
				sessionId,
				transports: [{ transport: 'webrtc', codecs: SUPPORTED_CODECS }, { transport: 'http', codecs: SUPPORTED_CODECS }],
				codecs: SUPPORTED_CODECS,
				intraRefresh: settings.mode === 'video',
				webrtc: {
					offer: encodeBase64(pc.localDescription!.sdp),
					dataChannel: WEBRTC_DATA_CHANNEL_LABEL
				}
			};

			const response = await fetch(`/api/agents/${options.agentId}/remote-desktop/transport`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(payload),
				signal: abort.signal
			});

			if (!response.ok) throw new Error(await response.text() || 'Negotiation failed');
			
			const negotiation = (await response.json()) as RemoteDesktopSessionNegotiationResponse;
			if (!negotiation.accepted || negotiation.transport !== 'webrtc' || !negotiation.webrtc?.answer) {
				throw new Error(negotiation.reason || 'WebRTC unavailable');
			}

			await pc.setRemoteDescription({ type: 'answer', sdp: decodeBase64(negotiation.webrtc.answer) });
			
			const negotiatedIce = toRtcIceServers(negotiation.webrtc.iceServers);
			if (negotiatedIce) {
				webrtcIceServers = negotiatedIce;
				pc.setConfiguration({ iceServers: negotiatedIce });
			}

			startWebRTCStats(negotiation.codec);
			return true;
		} catch (err) {
			if (!abort.signal.aborted) {
				console.warn('WebRTC negotiation failed', err);
				teardownWebRTC();
				options.onInfo('WebRTC transport unavailable; using HTTP stream.');
			}
			return false;
		} finally {
			webrtcNegotiating = false;
		}
	}

	function connectStream(sessionId: string) {
		if (!browser) return;
		if (eventSource) {
			eventSource.close();
		}

		const base = new URL(`/api/agents/${options.agentId}/remote-desktop/stream`, window.location.origin);
		base.searchParams.set('sessionId', sessionId);

		const source = new EventSource(base.toString());
		eventSource = source;

		source.addEventListener('session', (event: MessageEvent) => {
			try {
				const data = JSON.parse(event.data);
				if (data.session) options.onSessionUpdate(data.session);
			} catch (err) { console.error('Failed to parse session event', err); }
		});

		source.addEventListener('frame', (event: MessageEvent) => {
			if (webrtcVideoActive) return;
			try {
				const data = JSON.parse(event.data);
				if (data.frame) {
					options.onFrame(data.frame);
					if (data.frame.media) options.onMedia(data.frame.sessionId, data.frame.media);
				}
			} catch (err) { console.error('Failed to parse frame event', err); }
		});

		source.addEventListener('media', (event: MessageEvent) => {
			try {
				const data = JSON.parse(event.data) as RemoteDesktopStreamMediaMessage;
				if (data?.media) options.onMedia(data.sessionId, data.media);
			} catch (err) { console.error('Failed to parse media event', err); }
		});

		source.addEventListener('end', (event: MessageEvent) => {
			try {
				const data = JSON.parse(event.data);
				options.onEnd(data.reason);
			} catch { options.onEnd(); }
			disconnectStream();
		});

		source.onerror = () => {
			// TODO: Error handling
		};
	}

	function disconnectStream() {
		if (eventSource) {
			eventSource.close();
			eventSource = null;
		}
	}

	return {
		get webrtcVideoActive() { return webrtcVideoActive; },
		get webrtcAudioActive() { return webrtcAudioActive; },
		get webrtcNegotiating() { return webrtcNegotiating; },
		get transportDiagnostics() { return transportDiagnostics; },
		set webrtcVideoEl(el: HTMLVideoElement | null) { webrtcVideoEl = el; },
		negotiateWebRTC,
		connectStream,
		disconnectStream,
		teardownWebRTC
	};
}
