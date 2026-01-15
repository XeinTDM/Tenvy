import type { RequestHandler } from './$types';
import { error } from '@sveltejs/kit';
import { remoteDesktopManager } from '$lib/server/rat/remote-desktop';

export const GET: RequestHandler = ({ params, url, request }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	const sessionId = url.searchParams.get('sessionId') ?? undefined;
	const stream = remoteDesktopManager.subscribe(id, sessionId);

	const abort = request.signal;
	abort.addEventListener(
		'abort',
		() => {
			stream.cancel().catch(() => {
				// ignore
			});
		},
		{ once: true }
	);

	return new Response(stream, {
		headers: {
			'Content-Type': 'text/event-stream',
			'Cache-Control': 'no-cache',
			Connection: 'keep-alive'
		}
	});
};
