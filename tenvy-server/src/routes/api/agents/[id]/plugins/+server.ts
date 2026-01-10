import type { RequestHandler } from './$types';
import { _resolveClientPluginRequest } from '../../../clients/[id]/plugins/+server.js';

export const GET: RequestHandler = async ({ params, request, url }) => {
	return _resolveClientPluginRequest({ id: params.id }, request, url, { forceSnapshot: true });
};
