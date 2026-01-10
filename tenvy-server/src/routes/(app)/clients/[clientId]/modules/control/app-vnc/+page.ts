import type { PageLoad } from './$types';
import type { AppVncApplicationDescriptor, AppVncSessionState } from '$lib/types/app-vnc';

export const load = (async ({ fetch, params }) => {
	const id = params.clientId;
	let session: AppVncSessionState | null = null;
	let applications: AppVncApplicationDescriptor[] = [];

	try {
		const [sessionResp, appsResp] = await Promise.all([
			fetch(`/api/agents/${id}/app-vnc/session`),
			fetch('/api/app-vnc/apps')
		]);

		if (sessionResp.ok) {
			const payload = (await sessionResp.json()) as { session?: AppVncSessionState | null };
			session = payload.session ?? null;
		}

		if (appsResp.ok) {
			const payload = (await appsResp.json()) as {
				applications?: AppVncApplicationDescriptor[];
			};
			applications = payload.applications ?? [];
		}
	} catch {
		// fallbacks
	}

	return { session, applications };
}) satisfies PageLoad;
