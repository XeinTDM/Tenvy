import { createHash, randomUUID } from 'node:crypto';
import { mkdir, readFile, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import type { AppVncPlatform } from '$lib/types/app-vnc';
import { ensureParentDirectory, writeFileAtomic } from '../fs-utils';

export type AppVncSeedBundleKind = 'profile' | 'data';

export interface AppVncSeedBundleMetadata {
	id: string;
	appId: string;
	platform: AppVncPlatform;
	kind: AppVncSeedBundleKind;
	fileName: string;
	originalName: string;
	size: number;
	sha256: string;
	uploadedAt: string;
}

interface SeedManifestSchema {
	bundles: AppVncSeedBundleMetadata[];
}

const MAX_SEED_ARCHIVE_SIZE = 512 * 1024 * 1024;

const seedDirectory = process.env.TENVY_APP_VNC_RESOURCE_DIR
	? resolve(process.env.TENVY_APP_VNC_RESOURCE_DIR)
	: resolve(process.cwd(), 'resources/app-vnc');

const manifestPath = join(seedDirectory, 'manifest.json');

async function ensureSeedDirectory() {
	await mkdir(seedDirectory, { recursive: true });
}

async function readManifest(): Promise<SeedManifestSchema> {
	try {
		const data = await readFile(manifestPath, 'utf-8');
		const parsed = JSON.parse(data) as SeedManifestSchema;
		if (!Array.isArray(parsed?.bundles)) {
			return { bundles: [] };
		}
		return {
			bundles: parsed.bundles.map((bundle) => ({
				...bundle,
				uploadedAt: bundle.uploadedAt ?? new Date().toISOString()
			}))
		};
	} catch (err) {
		if ((err as NodeJS.ErrnoException).code === 'ENOENT') {
			return { bundles: [] };
		}
		throw err;
	}
}

async function writeManifest(manifest: SeedManifestSchema): Promise<void> {
	await ensureParentDirectory(manifestPath);
	await writeFileAtomic(manifestPath, JSON.stringify(manifest, null, 2));
}

export async function listSeedBundles(appId?: string): Promise<AppVncSeedBundleMetadata[]> {
	const manifest = await readManifest();
	if (!appId) {
		return manifest.bundles.slice();
	}
	return manifest.bundles.filter((bundle) => bundle.appId === appId);
}

function validateArchive(buffer: Buffer): void {
	if (buffer.length < 4) {
		throw new Error('Seed bundle too small');
	}
	if (buffer.length > MAX_SEED_ARCHIVE_SIZE) {
		throw new Error('Seed bundle exceeds maximum size');
	}
	const signature = buffer.subarray(0, 4);
	const ZIP_SIGNATURE = Buffer.from([0x50, 0x4b, 0x03, 0x04]);
	if (!signature.equals(ZIP_SIGNATURE)) {
		throw new Error('Seed bundle must be a ZIP archive');
	}
}

export async function registerSeedBundle(options: {
	appId: string;
	platform: AppVncPlatform;
	kind: AppVncSeedBundleKind;
	originalName: string;
	buffer: Buffer;
}): Promise<AppVncSeedBundleMetadata> {
	const appId = options.appId.trim();
	const platform = options.platform;
	const kind = options.kind;
	if (!appId) {
		throw new Error('Missing app identifier');
	}
	if (!platform) {
		throw new Error('Missing target platform');
	}
	if (kind !== 'profile' && kind !== 'data') {
		throw new Error('Invalid seed kind');
	}
	const buffer = options.buffer;
	validateArchive(buffer);
	await ensureSeedDirectory();
	const manifest = await readManifest();
	const id = randomUUID();
	const fileName = `${id}.zip`;
	const filePath = join(seedDirectory, fileName);
	const sha256 = createHash('sha256').update(buffer).digest('hex');
	const existing = manifest.bundles.find(
		(bundle) => bundle.appId === appId && bundle.platform === platform && bundle.kind === kind
	);
	if (existing && existing.sha256 === sha256 && existing.size === buffer.length) {
		return existing;
	}
	await writeFileAtomic(filePath, buffer);
	const metadata: AppVncSeedBundleMetadata = {
		id,
		appId,
		platform,
		kind,
		fileName,
		originalName: options.originalName || fileName,
		size: buffer.length,
		sha256,
		uploadedAt: new Date().toISOString()
	};
	manifest.bundles = manifest.bundles.filter(
		(bundle) => !(bundle.appId === appId && bundle.platform === platform && bundle.kind === kind)
	);
	manifest.bundles.push(metadata);
	await writeManifest(manifest);
	if (existing && existing.fileName && existing.fileName !== fileName) {
		await rm(join(seedDirectory, existing.fileName), { force: true });
	}
	return metadata;
}

export async function removeSeedBundle(id: string): Promise<void> {
	const manifest = await readManifest();
	const entry = manifest.bundles.find((bundle) => bundle.id === id);
	if (!entry) {
		return;
	}
	const filePath = join(seedDirectory, entry.fileName);
	manifest.bundles = manifest.bundles.filter((bundle) => bundle.id !== id);
	await writeManifest(manifest);
	await rm(filePath, { force: true });
}

export async function getSeedBundle(id: string): Promise<AppVncSeedBundleMetadata | null> {
	const manifest = await readManifest();
	const entry = manifest.bundles.find((bundle) => bundle.id === id);
	return entry ?? null;
}

export function resolveSeedFilePath(bundle: AppVncSeedBundleMetadata): string {
	return join(seedDirectory, bundle.fileName);
}

export async function resolveSeedPlan(
	agentId: string,
	appId: string,
	platform: AppVncPlatform
): Promise<{ profileSeed?: string; dataRoot?: string }> {
	if (!appId || !platform) {
		return {};
	}
	const manifest = await readManifest();
	const profile = manifest.bundles.find(
		(bundle) => bundle.appId === appId && bundle.platform === platform && bundle.kind === 'profile'
	);
	const data = manifest.bundles.find(
		(bundle) => bundle.appId === appId && bundle.platform === platform && bundle.kind === 'data'
	);
	const result: { profileSeed?: string; dataRoot?: string } = {};
	if (profile) {
		const query = agentId ? `?agent=${encodeURIComponent(agentId)}` : '';
		result.profileSeed = `/api/app-vnc/seeds/${profile.id}/download${query}`;
	}
	if (data) {
		const query = agentId ? `?agent=${encodeURIComponent(agentId)}` : '';
		result.dataRoot = `/api/app-vnc/seeds/${data.id}/download${query}`;
	}
	return result;
}
