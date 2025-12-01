import { env } from '$env/dynamic/private';
import { readdir, readFile } from 'node:fs/promises';
import type { Dirent } from 'node:fs';
import { isAbsolute, join, resolve } from 'node:path';
import type {
	PluginManifest,
	PluginSignatureVerificationError,
	PluginSignatureVerificationResult,
	PluginSignatureVerificationSummary
} from '../../../../shared/types/plugin-manifest';
import {
	validatePluginManifest,
	verifyPluginSignature
} from '../../../../shared/types/plugin-manifest';
import { getVerificationOptions } from '$lib/server/plugins/signature-policy.js';
import {
	createPluginRegistryStore,
	type PluginRegistryStore,
	type PluginRegistryRecord
} from '$lib/server/plugins/registry-store.js';
import {
	summarizeVerificationFailure,
	summarizeVerificationSuccess
} from '$lib/server/plugins/signature-summary.js';
import { resolveProjectPath } from '$lib/server/path-utils.js';

export interface LoadedPluginManifest {
	source: string;
	manifest: PluginManifest;
	verification: PluginSignatureVerificationSummary;
	raw: string;
	registry?: {
		id: string;
		approvalStatus: string;
		approvedAt: string | null;
	};
}

const defaultManifestDirectory = resolveProjectPath('resources', 'plugin-manifests');

const resolveRelativeToRoot = (value: string): string =>
	isAbsolute(value) ? value : resolveProjectPath(value);

const isJsonFile = (entryName: string): boolean => entryName.toLowerCase().endsWith('.json');

const resolveDirectory = (directory?: string): string => {
	if (directory && directory.trim().length > 0) {
		return resolveRelativeToRoot(directory.trim());
	}

	if (env.TENVY_PLUGIN_MANIFEST_DIR && env.TENVY_PLUGIN_MANIFEST_DIR.trim().length > 0) {
		return resolveRelativeToRoot(env.TENVY_PLUGIN_MANIFEST_DIR.trim());
	}

	return defaultManifestDirectory;
};

type LoadOptions = {
	directory?: string;
	registryStore?: PluginRegistryStore;
};

const statusPriority: Record<string, number> = {
	approved: 2,
	pending: 1,
	rejected: 0
};

const selectRegistryRecords = (records: PluginRegistryRecord[]): PluginRegistryRecord[] => {
	const latest = new Map<string, PluginRegistryRecord>();

	for (const record of records) {
		if (record.approvalStatus === 'rejected') {
			continue;
		}

		const existing = latest.get(record.pluginId);
		if (!existing) {
			latest.set(record.pluginId, record);
			continue;
		}

		const existingPriority = statusPriority[existing.approvalStatus] ?? 0;
		const candidatePriority = statusPriority[record.approvalStatus] ?? 0;

		if (candidatePriority > existingPriority) {
			latest.set(record.pluginId, record);
			continue;
		}

		if (candidatePriority === existingPriority) {
			const existingPublished = existing.publishedAt?.getTime() ?? 0;
			const candidatePublished = record.publishedAt?.getTime() ?? 0;
			if (candidatePublished > existingPublished) {
				latest.set(record.pluginId, record);
			}
		}
	}

	return Array.from(latest.values());
};

export async function loadPluginManifests(
	options: LoadOptions = {}
): Promise<LoadedPluginManifest[]> {
	if (options.directory) {
		const directory = resolveDirectory(options.directory);

		let entries: Dirent[];
		try {
			entries = await readdir(directory, { withFileTypes: true });
		} catch (error) {
			const err = error as NodeJS.ErrnoException;
			if (err?.code === 'ENOENT') {
				return [];
			}
			throw err;
		}

		const manifests: LoadedPluginManifest[] = [];
		const verificationOptions = getVerificationOptions();

		for (const entry of entries) {
			if (!entry.isFile() || !isJsonFile(entry.name)) {
				continue;
			}

			const source = join(directory, entry.name);
			try {
				const fileContents = await readFile(source, 'utf8');
				const manifest = JSON.parse(fileContents) as PluginManifest;
				const errors = validatePluginManifest(manifest);

				if (errors.length > 0) {
					console.warn(`Skipping invalid plugin manifest at ${source}`, errors);
					continue;
				}
				let verification: PluginSignatureVerificationSummary;

				try {
					const result = await verifyPluginSignature(manifest, verificationOptions);
					verification = summarizeVerificationSuccess(manifest, result);
					if (!verification.trusted) {
						console.warn(
							`Plugin manifest ${manifest.id} marked as ${verification.status} during verification`
						);
					}
				} catch (error) {
					const err = error as PluginSignatureVerificationError | Error;
					verification = summarizeVerificationFailure(manifest, err);
					console.warn(
						`Plugin manifest ${manifest.id} failed signature verification (${verification.status}):`,
						err
					);
				}

				manifests.push({ source, manifest, verification, raw: fileContents });
			} catch (error) {
				console.warn(`Failed to load plugin manifest at ${source}`, error);
			}
		}

		manifests.sort((a, b) => a.manifest.name.localeCompare(b.manifest.name));

		return manifests;
	}

	const store = options.registryStore ?? createPluginRegistryStore();
	const records = await store.list();
	const selected = selectRegistryRecords(records);
	const manifests: LoadedPluginManifest[] = [];
	const verificationOptions = getVerificationOptions();

	for (const record of selected) {
		const manifest = record.manifest;
		const raw = record.raw ?? JSON.stringify(manifest);
		let verification: PluginSignatureVerificationSummary;

		try {
			const result = await verifyPluginSignature(manifest, verificationOptions);
			verification = summarizeVerificationSuccess(manifest, result);
		} catch (error) {
			const err = error as PluginSignatureVerificationError | Error;
			verification = summarizeVerificationFailure(manifest, err);
		}

		manifests.push({
			source: `registry:${record.id}`,
			manifest,
			verification,
			raw,
			registry: {
				id: record.id,
				approvalStatus: record.approvalStatus,
				approvedAt: record.approvedAt ? record.approvedAt.toISOString() : null
			}
		});
	}

	manifests.sort((a, b) => a.manifest.name.localeCompare(b.manifest.name));
	return manifests;
}
