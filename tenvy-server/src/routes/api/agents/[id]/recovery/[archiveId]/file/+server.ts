import { readFile } from 'fs/promises';
import JSZip from 'jszip';
import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import {
	RecoveryArchiveMetadataError,
	getRecoveryArchive,
	getRecoveryArchiveFilePath
} from '$lib/server/recovery/storage';
import { normalizeArchiveEntryPath } from '$lib/server/recovery/validation';

const MAX_PREVIEW_BYTES = 2 * 1024 * 1024;

function isLikelyText(buffer: Uint8Array): boolean {
	if (buffer.length === 0) {
		return true;
	}
	let nonPrintable = 0;
	for (const byte of buffer) {
		if (byte === 0) {
			return false;
		}
		if (byte < 0x09) {
			nonPrintable += 1;
		} else if (byte >= 0x0e && byte < 0x20) {
			nonPrintable += 1;
		}
	}
	return nonPrintable < buffer.length * 0.1;
}

export const GET: RequestHandler = async ({ params, url }) => {
	const id = params.id;
	const archiveId = params.archiveId;
	if (!id || !archiveId) {
		throw error(400, 'Missing identifiers');
	}

	const targetPath = url.searchParams.get('path');
	if (!targetPath) {
		throw error(400, 'Missing path parameter');
	}
	let normalizedPath: string;
	try {
		normalizedPath = normalizeArchiveEntryPath(targetPath);
	} catch (err) {
		const message = err instanceof Error ? err.message : 'Invalid path parameter';
		throw error(400, message);
	}

	try {
		const archive = await getRecoveryArchive(id, archiveId);
		const entry = archive.manifest.find((item) => item.path === normalizedPath);
		if (!entry) {
			throw error(404, 'Entry not found in manifest');
		}
		if (entry.type !== 'file') {
			throw error(400, 'Requested entry is not a file');
		}
		if (entry.size > MAX_PREVIEW_BYTES) {
			throw error(413, 'File exceeds preview limit');
		}

		const zipPath = await getRecoveryArchiveFilePath(id, archiveId);
		const zipData = await readFile(zipPath);
		const zip = await JSZip.loadAsync(zipData);
		const file = zip.file(normalizedPath);
		if (!file) {
			throw error(404, 'File not found in archive');
		}

		const buffer = new Uint8Array(await file.async('nodebuffer'));
		if (buffer.byteLength > MAX_PREVIEW_BYTES) {
			throw error(413, 'File exceeds preview limit');
		}

		if (url.searchParams.get('download') === '1') {
			const filename = normalizedPath.split('/').pop() ?? 'file.bin';
			return new Response(Buffer.from(buffer), {
				headers: {
					'Content-Type': 'application/octet-stream',
					'Content-Length': String(buffer.byteLength),
					'Content-Disposition': `attachment; filename="${encodeURIComponent(filename)}"`
				}
			});
		}

		let encoding: 'utf-8' | 'base64' = 'base64';
		let content: string;
		if (isLikelyText(buffer)) {
			encoding = 'utf-8';
			content = Buffer.from(buffer).toString('utf-8');
		} else {
			content = Buffer.from(buffer).toString('base64');
		}

		return json({
			path: normalizedPath,
			encoding,
			content,
			size: buffer.byteLength
		});
	} catch (err) {
		if ((err as NodeJS.ErrnoException).code === 'ENOENT') {
			throw error(404, 'Recovery archive not found');
		}
		if (err instanceof Response) {
			throw err;
		}
		if (err instanceof RecoveryArchiveMetadataError) {
			console.error('Recovery archive metadata validation failed', err);
			throw error(500, 'Recovery archive metadata failed validation');
		}
		throw error(500, 'Failed to read archive entry');
	}
};
