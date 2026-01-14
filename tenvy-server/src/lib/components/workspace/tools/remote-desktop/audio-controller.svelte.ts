import { browser } from '$app/environment';
import type { RemoteDesktopMediaSample } from '$lib/types/remote-desktop';

const REMOTE_DESKTOP_AUDIO_SAMPLE_RATE = 48_000;

export function createAudioController() {
	let audioContext: AudioContext | null = null;
	let audioQueueTime = 0;

	async function ensureAudioPlaybackContext(): Promise<boolean> {
		if (!browser) return false;
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
			try { await audioContext.resume(); }
			catch (err) { console.warn('Failed to resume remote desktop audio context', err); }
		}
		return true;
	}

	function decodePcmSample(data: string | Uint8Array): Int16Array | null {
		try {
			if (data instanceof Uint8Array) return new Int16Array(data.buffer, data.byteOffset, data.byteLength);
			const binary = atob(data);
			if (binary.length % 2 !== 0) return null;
			const buffer = new ArrayBuffer(binary.length);
			const bytes = new Uint8Array(buffer);
			for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
			return new Int16Array(buffer);
		} catch (err) {
			console.warn('Failed to decode remote desktop PCM sample', err);
			return null;
		}
	}

	function scheduleAudioPlayback(pcm: Int16Array, channels: number) {
		if (!audioContext) return;
		const normalizedChannels = Math.max(1, Math.min(2, channels));
		const frameCount = Math.floor(pcm.length / normalizedChannels);
		if (frameCount <= 0) return;
		const buffer = audioContext.createBuffer(normalizedChannels, frameCount, REMOTE_DESKTOP_AUDIO_SAMPLE_RATE);
		for (let channel = 0; channel < normalizedChannels; channel += 1) {
			const channelData = buffer.getChannelData(channel);
			for (let frame = 0; frame < frameCount; frame += 1) {
				const sampleIndex = frame * normalizedChannels + channel;
				channelData[frame] = pcm[sampleIndex] / 32768;
			}
		}
		const source = audioContext.createBufferSource();
		source.buffer = buffer;
		source.connect(audioContext.destination);
		const startAt = Math.max(audioContext.currentTime + 0.05, audioQueueTime);
		source.start(startAt);
		audioQueueTime = startAt + buffer.duration;
	}

	async function handleAudioSample(sample: RemoteDesktopMediaSample) {
		if (!browser) return;
		if (sample.format !== 'pcm' && sample.codec !== 'pcm') return;
		if (!(await ensureAudioPlaybackContext())) return;
		const pcm = decodePcmSample(sample.data);
		if (!pcm) return;
		const channels = pcm.length % 2 === 0 ? 2 : 1;
		scheduleAudioPlayback(pcm, channels);
	}

	function clear() {
		if (audioContext) {
			audioContext.close().catch(() => {});
			audioContext = null;
		}
		audioQueueTime = 0;
	}

	return {
		handleAudioSample,
		clear,
		ensureAudioPlaybackContext
	};
}
