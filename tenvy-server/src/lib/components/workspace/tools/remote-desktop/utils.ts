import { browser } from '$app/environment';
import type { RemoteDesktopWebRTCICEServer } from '$lib/types/remote-desktop';

export function encodeBase64(value: string): string {
	const encoder = new TextEncoder();
	const bytes = encoder.encode(value);
	let binary = '';
	for (const byte of bytes) {
		binary += String.fromCharCode(byte);
	}
	return btoa(binary);
}

export function decodeBase64(value: string): string {
	const binary = atob(value);
	const bytes = new Uint8Array(binary.length);
	for (let index = 0; index < binary.length; index += 1) {
		bytes[index] = binary.charCodeAt(index);
	}
	const decoder = new TextDecoder();
	return decoder.decode(bytes);
}

export function toRtcIceServers(servers: RemoteDesktopWebRTCICEServer[] | undefined) {
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

export async function waitForPeerIceGathering(pc: RTCPeerConnection): Promise<void> {
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

export const clamp = (value: number, min: number, max: number) => {
	if (Number.isNaN(value)) return min;
	if (value < min) return min;
	if (value > max) return max;
	return value;
};
