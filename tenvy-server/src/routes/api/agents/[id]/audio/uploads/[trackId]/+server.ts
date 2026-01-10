import { error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { audioBridgeManager } from '$lib/server/rat/audio';
import { join } from 'node:path';
import { stat, readFile } from 'node:fs/promises';

export const GET: RequestHandler = async ({ params, setHeaders, url }) => {
	const { id, trackId } = params;
	if (!id || !trackId) {
		throw error(400, 'Missing identifiers');
	}

	const format = url.searchParams.get('format');

	if (format === 'pcm') {
		const result = await audioBridgeManager.getUploadPCM(id, trackId);
		if (!result) {
			throw error(404, 'Audio track not found or failed to decode');
		}
		setHeaders({
			'Content-Length': result.data.byteLength.toString(),
			'Content-Type': 'application/octet-stream'
		});
		return new Response(result.data as BodyInit);
	}

	const record = audioBridgeManager.getUpload(id, trackId);
	if (!record) {
		throw error(404, 'Audio track not found');
	}

	const directory = await audioBridgeManager.ensureUploadDirectory(id);
	const path = join(directory, record.storedName);
	let fileStat;
	try {
		fileStat = await stat(path);
	} catch {
		throw error(404, 'Audio track file not found');
	}
	if (!fileStat.isFile()) {
		throw error(404, 'Audio track file not found');
	}

	const buffer = await readFile(path);
	const binary = new Uint8Array(buffer);
	const headers: Record<string, string> = {
		'Content-Length': binary.byteLength.toString()
	};
	if (record.contentType) {
		headers['Content-Type'] = record.contentType;
	}
	setHeaders(headers);
	return new Response(binary);
};
