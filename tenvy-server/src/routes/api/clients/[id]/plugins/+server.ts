import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { registry, RegistryError } from '$lib/server/rat/store.js';
import { createPluginRepository } from '$lib/data/plugins.js';
import { loadPluginManifests } from '$lib/data/plugin-manifests.js';
import { buildClientPlugin, type ClientPlugin } from '$lib/data/client-plugin-view.js';
import { getBearerToken } from '$lib/server/http/bearer.js';
import { telemetryStore } from '../../../agents/[id]/plugins/_shared.js';

const repository = createPluginRepository();

const SNAPSHOT_MEDIA_TYPE = 'application/vnd.tenvy.plugin-manifest+json';

type ClientPluginRequestOptions = {
	forceSnapshot?: boolean;
};

export async function _resolveClientPluginRequest(
	params: { id?: string },
	request: Request,
	url: URL,
	options: ClientPluginRequestOptions = {}
): Promise<Response> {
	const { id } = params;
	if (!id) {
		throw error(400, 'Missing client identifier');
	}

	const wantsSnapshot =
		options.forceSnapshot === true ||
		url.searchParams.get('format') === 'snapshot' ||
		acceptsSnapshot(request.headers.get('accept'));

	if (wantsSnapshot) {
		const token = getBearerToken(request.headers.get('authorization'));
		if (!token) {
			throw error(401, 'Missing agent key');
		}

		try {
			registry.authorizeAgent(id, token);
		} catch (err) {
			if (err instanceof RegistryError) {
				throw error(err.status, err.message);
			}
			throw error(500, 'Failed to authorize agent');
		}

		const snapshot = await telemetryStore.getManifestSnapshot();
		return json(snapshot, {
			headers: {
				'content-type': SNAPSHOT_MEDIA_TYPE,
				'cache-control': 'no-store'
			}
		});
	}

	try {
		registry.getAgent(id);
	} catch {
		throw error(404, 'Client not found');
	}

	const [manifestRecords, pluginViews, telemetryRecords] = await Promise.all([
		loadManifests(),
		repository.list(),
		telemetryStore.listAgentPlugins(id)
	]);

	const manifestIndex = new Map(
		manifestRecords.map((record) => [record.manifest.id, record.manifest])
	);
	const telemetryIndex = new Map(telemetryRecords.map((record) => [record.pluginId, record]));

	const plugins: ClientPlugin[] = [];

	for (const view of pluginViews) {
		const manifest = manifestIndex.get(view.id);
		if (!manifest) {
			continue;
		}

		const telemetry = telemetryIndex.get(view.id);
		plugins.push(buildClientPlugin(manifest, view, telemetry));
	}

	return json({ plugins });
}

export const GET: RequestHandler = async ({ params, request, url }) => {
	return _resolveClientPluginRequest(params, request, url);
};

async function loadManifests() {
	return loadPluginManifests();
}

function acceptsSnapshot(accept: string | null): boolean {
	if (!accept) {
		return false;
	}

	return accept
		.split(',')
		.map((part) => part.split(';')[0]?.trim().toLowerCase())
		.some((mime) => mime === SNAPSHOT_MEDIA_TYPE);
}
