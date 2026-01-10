import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { requireViewer } from '$lib/server/authorization';
import { registry, RegistryError } from '$lib/server/rat/store';
import { downloadCatalogueSchema } from '$lib/types/downloads';

export const GET: RequestHandler = ({ params, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	requireViewer(locals.user);

	try {
		const downloads = registry.getDownloadsCatalogue(id);
		const payload = downloadCatalogueSchema.parse(downloads);
		return json({ downloads: payload });
	} catch (err) {
		if (err && typeof err === 'object' && 'isRegistryError' in err) {
			const regErr = err as { status: number; message: string };
			throw error(regErr.status, regErr.message);
		}
		throw error(500, 'Failed to load downloads catalogue');
	}
};
