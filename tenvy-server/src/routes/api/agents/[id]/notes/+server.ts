import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { registry, RegistryError } from '$lib/server/rat/store';
import { requireOperator } from '$lib/server/authorization';
import type { NoteSyncRequest } from '../../../../../../../shared/types/notes';
import type { AgentOperatorNote } from '../../../../../../../shared/types/agent';

function getBearerToken(header: string | null): string | undefined {
	if (!header) {
		return undefined;
	}
	const match = header.match(/^Bearer\s+(.+)$/i);
	return match?.[1]?.trim();
}

function parseOperatorPayload(input: unknown): { note: string; tags: string[] } {
	if (!input || typeof input !== 'object') {
		return { note: '', tags: [] };
	}

	const { note, tags } = input as { note?: unknown; tags?: unknown };
	const normalizedNote = typeof note === 'string' ? note : '';
	const normalizedTags = Array.isArray(tags)
		? tags.map((tag) => `${tag ?? ''}`.trim()).filter((tag) => tag.length > 0)
		: [];

	return { note: normalizedNote, tags: normalizedTags };
}

function toResponsePayload(note: AgentOperatorNote): AgentOperatorNote {
	return {
		note: note.note,
		tags: [...note.tags],
		updatedAt: note.updatedAt ?? null,
		updatedBy: note.updatedBy ?? null
	} satisfies AgentOperatorNote;
}

export const GET: RequestHandler = async ({ params, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	requireOperator(locals.user);

	try {
		const note = registry.getOperatorNote(id);
		return json(toResponsePayload(note));
	} catch (err) {
		if (err instanceof RegistryError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to load operator note');
	}
};

export const POST: RequestHandler = async ({ params, request, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	let body: unknown;
	try {
		body = await request.json();
	} catch {
		throw error(400, 'Invalid note payload');
	}

	const token = getBearerToken(request.headers.get('authorization'));
	if (token) {
		const payload = body as NoteSyncRequest;
		try {
			const notes = registry.syncSharedNotes(id, token, payload?.notes ?? []);
			return json({ notes });
		} catch (err) {
			if (err instanceof RegistryError) {
				throw error(err.status, err.message);
			}
			throw error(500, 'Failed to sync notes');
		}
	}

	if (!locals.user) {
		throw error(401, 'Missing agent key');
	}

	const operator = requireOperator(locals.user);
	const payload = parseOperatorPayload(body);

	try {
		const note = registry.updateOperatorNote(id, payload, { operatorId: operator.id });
		return json(toResponsePayload(note));
	} catch (err) {
		if (err instanceof RegistryError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to save operator note');
	}
};
