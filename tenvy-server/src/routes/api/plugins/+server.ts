import { createHash } from 'node:crypto';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { isAbsolute, join, resolve, sep } from 'node:path';
import { error, json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { createPluginRepository } from '$lib/data/plugins.js';
import { loadPluginManifests } from '$lib/data/plugin-manifests.js';
import { getVerificationOptions } from '$lib/server/plugins/signature-policy.js';
import { requireDeveloper } from '$lib/server/authorization.js';
import {
	ArchiveExtractionError,
	extractPluginArchive,
	readExtractedManifest
} from '$lib/server/plugins/archive.js';
import {
	createPluginRegistryStore,
	PluginRegistryError
} from '$lib/server/plugins/registry-store.js';
import {
	validatePluginManifest,
	verifyPluginSignature,
	type PluginManifest,
	type PluginSignatureVerificationSummary
} from '../../../../../shared/types/plugin-manifest';
import { summarizeVerificationSuccess } from '$lib/server/plugins/signature-summary.js';
import { resolveProjectPath } from '$lib/server/path-utils.js';

const MANIFEST_FILE_EXTENSION = '.json';
const PLUGIN_ID_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9._-]*$/;

const repository = createPluginRepository();
const registryStore = createPluginRegistryStore();

const resolveManifestDirectory = (): string => {
	const configured = process.env.TENVY_PLUGIN_MANIFEST_DIR?.trim();
	if (configured && configured.length > 0) {
		return isAbsolute(configured) ? configured : resolveProjectPath(configured);
	}
	return resolveProjectPath('resources', 'plugin-manifests');
};

const resolveArtifactDirectory = (): string => {
	const configured = process.env.TENVY_PLUGIN_ARTIFACT_DIR?.trim();
	if (configured && configured.length > 0) {
		return isAbsolute(configured) ? configured : resolveProjectPath(configured);
	}
	return resolveProjectPath('var', 'plugin-artifacts');
};

const requireFile = (value: FormDataEntryValue | null, name: string): File => {
	if (!value || !(value instanceof File)) {
		throw error(400, { message: `Missing ${name} upload` });
	}
	if (value.size === 0) {
		throw error(400, { message: `${name} upload is empty` });
	}
	return value;
};

const ensurePluginId = (manifest: PluginManifest): string => {
	const pluginId = manifest.id?.trim();
	if (!pluginId) {
		throw error(400, { message: 'Plugin manifest is missing id' });
	}
	if (pluginId.includes('..') || !PLUGIN_ID_PATTERN.test(pluginId)) {
		throw error(400, {
			message: 'Plugin id may only contain letters, numbers, dot, hyphen, and underscore characters'
		});
	}
	return pluginId;
};

const ensureArtifactReference = (manifest: PluginManifest): string => {
	const artifact = manifest.package?.artifact?.trim() ?? '';
	if (!artifact) {
		throw error(400, { message: 'Plugin manifest is missing package artifact reference' });
	}
	if (artifact.includes('/') || artifact.includes('\\')) {
		throw error(400, {
			message: 'Plugin artifact reference must not include directory separators'
		});
	}
	return artifact;
};

const writeManifest = async (directory: string, pluginId: string, manifest: PluginManifest) => {
	await mkdir(directory, { recursive: true });
	const target = join(directory, `${pluginId}${MANIFEST_FILE_EXTENSION}`);
	const normalizedBase = directory.endsWith(sep) ? directory : `${directory}${sep}`;
	const resolved = resolve(target);
	if (!resolved.startsWith(normalizedBase)) {
		throw error(400, { message: 'Resolved manifest path is outside the manifest directory' });
	}
	await writeFile(target, `${JSON.stringify(manifest, null, 2)}\n`, 'utf8');
};

const writeArtifact = async (directory: string, fileName: string, payload: Uint8Array) => {
	await mkdir(directory, { recursive: true });
	const target = join(directory, fileName);
	const normalizedBase = directory.endsWith(sep) ? directory : `${directory}${sep}`;
	const resolved = resolve(target);
	if (!resolved.startsWith(normalizedBase)) {
		throw error(400, { message: 'Resolved artifact path is outside the artifact directory' });
	}
	await writeFile(target, payload);
};

export const GET: RequestHandler = async () => {
	const plugins = await repository.list();
	return json({ plugins });
};

export const POST: RequestHandler = async ({ locals, request }) => {
	requireDeveloper(locals.user);

	const contentType = request.headers.get('content-type') ?? '';
	if (!contentType.toLowerCase().includes('multipart/form-data')) {
		throw error(415, { message: 'Expected multipart form data upload' });
	}

	const formData = await request.formData();
	const artifactUpload = requireFile(formData.get('artifact'), 'artifact');
	const packageBuffer = Buffer.from(await artifactUpload.arrayBuffer());

	let archive: Awaited<ReturnType<typeof extractPluginArchive>> | null = null;
	try {
		archive = await extractPluginArchive(packageBuffer, { fileName: artifactUpload.name });
	} catch (err) {
		if (err instanceof ArchiveExtractionError) {
			console.warn('Rejected plugin upload: invalid archive', err);
			throw error(400, { message: err.message });
		}
		throw err;
	}

	if (!archive) {
		throw error(500, { message: 'Archive extraction failed' });
	}

	try {
		let manifestContent: string;
		try {
			manifestContent = await readExtractedManifest(archive);
		} catch (err) {
			if (err instanceof ArchiveExtractionError) {
				console.warn('Rejected plugin upload: manifest missing from archive', err);
				throw error(400, { message: err.message });
			}
			throw err;
		}
		let manifest: PluginManifest;
		try {
			manifest = JSON.parse(manifestContent) as PluginManifest;
		} catch (err) {
			console.warn('Rejected plugin upload: manifest is not valid JSON', err);
			throw error(400, { message: 'Manifest must be valid JSON' });
		}

		const validationErrors = validatePluginManifest(manifest);
		if (validationErrors.length > 0) {
			console.warn('Rejected plugin upload: manifest failed validation', {
				errors: validationErrors
			});
			const details = validationErrors.join(', ');
			throw error(400, details ? `Invalid plugin manifest: ${details}` : 'Invalid plugin manifest');
		}

		const pluginId = ensurePluginId(manifest);
		const manifestDirectory = resolveManifestDirectory();
		const existingRecords = await loadPluginManifests({ directory: manifestDirectory });
		const existingRecord = existingRecords.find((record) => record.manifest.id === pluginId);
		if (existingRecord && existingRecord.manifest.version === manifest.version) {
			console.warn('Rejected plugin upload: duplicate version', {
				pluginId,
				version: manifest.version
			});
			throw error(409, {
				message: `Plugin ${pluginId} version ${manifest.version} already exists`
			});
		}

		const artifactName = ensureArtifactReference(manifest);
		const extractedArtifact = archive.entriesByBasename.get(artifactName);
		if (!extractedArtifact) {
			console.warn('Rejected plugin upload: artifact missing from archive', {
				pluginId,
				artifactName
			});
			throw error(400, { message: `Plugin artifact ${artifactName} is missing from archive` });
		}

		const artifactBuffer = await readFile(extractedArtifact.absolutePath);
		const artifactHash = createHash('sha256').update(artifactBuffer).digest('hex');
		const manifestHash = manifest.package?.hash?.trim().toLowerCase();
		if (!manifestHash) {
			throw error(400, { message: 'Plugin manifest is missing package hash' });
		}

		if (artifactHash !== manifestHash) {
			console.warn('Rejected plugin upload: artifact hash mismatch', {
				pluginId,
				expected: manifestHash,
				actual: artifactHash
			});
			throw error(400, {
				message: 'Artifact hash does not match manifest package hash'
			});
		}

		if (manifest.package) {
			manifest.package.sizeBytes = artifactBuffer.byteLength;
		}

		let signatureSummary: PluginSignatureVerificationSummary;
		try {
			const result = await verifyPluginSignature(manifest, getVerificationOptions());
			signatureSummary = summarizeVerificationSuccess(manifest, result);
			if (!signatureSummary.trusted) {
				console.warn(
					`Plugin manifest ${manifest.id} marked as ${signatureSummary.status} during verification`
				);
			}
		} catch (err) {
			console.warn('Rejected plugin upload: signature verification failed', err);
			throw error(400, {
				message: err instanceof Error ? err.message : 'Signature verification failed'
			});
		}

		const artifactDirectory = resolveArtifactDirectory();
		const artifactPath = join(artifactDirectory, artifactName);
		const manifestPath = join(manifestDirectory, `${pluginId}${MANIFEST_FILE_EXTENSION}`);
		const writtenPaths: string[] = [];
		const recordWritten = (path: string) => {
			if (!writtenPaths.includes(path)) {
				writtenPaths.push(path);
			}
		};
		const cleanupWrittenFiles = async () => {
			await Promise.allSettled(writtenPaths.map((path) => rm(path, { force: true })));
		};

		try {
			await writeArtifact(artifactDirectory, artifactName, artifactBuffer);
			recordWritten(artifactPath);
			await writeManifest(manifestDirectory, pluginId, manifest);
			recordWritten(manifestPath);

			await registryStore.publish({
				manifest,
				actorId: locals.user?.id ?? null,
				skipValidation: true,
				preverifiedSummary: signatureSummary
			});
		} catch (err) {
			if (writtenPaths.length > 0) {
				await cleanupWrittenFiles();
			}
			if (err instanceof PluginRegistryError) {
				throw error(400, { message: err.message });
			}
			throw err;
		}

		const plugin = await repository.get(pluginId);

		console.info('Accepted plugin upload', {
			pluginId,
			version: manifest.version,
			replacedVersion: existingRecord?.manifest.version ?? null
		});

		return json(
			{
				plugin,
				approvalStatus: plugin.approvalStatus ?? 'pending'
			},
			{ status: existingRecord ? 200 : 201 }
		);
	} finally {
		await archive.cleanup();
	}
};
