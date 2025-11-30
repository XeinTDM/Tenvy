import { createHash, randomUUID } from 'node:crypto';
import { and, eq, desc } from 'drizzle-orm';
import { db } from '$lib/server/db/index.js';
import { pluginRegistryEntry as registryTable } from '$lib/server/db/schema.js';
import {
	validatePluginManifest,
	verifyPluginSignature,
	type PluginManifest,
	type PluginSignatureVerificationError,
	type PluginSignatureVerificationResult,
	type PluginSignatureVerificationSummary,
	type PluginApprovalStatus
} from '../../../../../shared/types/plugin-manifest';
import { getVerificationOptions } from '$lib/server/plugins/signature-policy.js';
import {
	createPluginRuntimeStore,
	type PluginRuntimeStore
} from '$lib/server/plugins/runtime-store.js';
import {
	summarizeVerificationFailure,
	summarizeVerificationSuccess
} from '$lib/server/plugins/signature-summary.js';

export type PluginRegistryStatus = Exclude<PluginApprovalStatus, 'pending'> | 'pending';

export interface PluginRegistryMetadata {
	[key: string]: unknown;
}

export interface PluginRegistryRecord {
	id: string;
	pluginId: string;
	version: string;
	manifest: PluginManifest;
	raw: string;
	manifestDigest: string;
	artifactHash: string | null;
	artifactSizeBytes: number | null;
	approvalStatus: PluginRegistryStatus;
	publishedAt: Date;
	publishedBy: string | null;
	approvedAt: Date | null;
	approvedBy: string | null;
	approvalNote: string | null;
	revokedAt: Date | null;
	revokedBy: string | null;
	revocationReason: string | null;
	metadata: PluginRegistryMetadata | null;
	createdAt: Date;
	updatedAt: Date;
}

export interface PublishPluginInput {
	manifest: PluginManifest;
	actorId?: string | null;
	metadata?: PluginRegistryMetadata | null;
	approvalNote?: string | null;
	skipValidation?: boolean;
	preverifiedSummary?: PluginSignatureVerificationSummary | null;
}

export interface ApprovePluginInput {
	id: string;
	actorId: string;
	note?: string | null;
}

export interface RevokePluginInput {
	id: string;
	actorId: string;
	reason?: string | null;
}

export class PluginRegistryError extends Error {
	constructor(message: string) {
		super(message);
		this.name = 'PluginRegistryError';
	}
}

const verificationOptions = () => getVerificationOptions();

const serializeMetadata = (metadata: PluginRegistryMetadata | null | undefined): string | null => {
	if (!metadata || Object.keys(metadata).length === 0) {
		return null;
	}
	try {
		return JSON.stringify(metadata);
	} catch (error) {
		console.warn('Failed to serialize plugin registry metadata', error);
		return null;
	}
};

const parseMetadata = (payload: string | null | undefined): PluginRegistryMetadata | null => {
	if (!payload) {
		return null;
	}
	try {
		const parsed = JSON.parse(payload) as PluginRegistryMetadata;
		return parsed;
	} catch (error) {
		console.warn('Failed to parse plugin registry metadata', error);
		return null;
	}
};

const computeManifestDigest = (manifestJson: string): string =>
	createHash('sha256').update(manifestJson, 'utf8').digest('hex');

const toDateValue = (value: Date | string | number | null | undefined): Date | null => {
	if (value == null) {
		return null;
	}
	if (value instanceof Date) {
		return value;
	}
	if (typeof value === 'number') {
		const numeric = new Date(value);
		return Number.isNaN(numeric.getTime()) ? null : numeric;
	}
	if (typeof value === 'string') {
		const trimmed = value.trim();
		if (trimmed.length === 0) {
			return null;
		}
		const parsed = new Date(trimmed);
		return Number.isNaN(parsed.getTime()) ? null : parsed;
	}
	return null;
};

const toRegistryRecord = (row: typeof registryTable.$inferSelect): PluginRegistryRecord => {
	const parsedManifest = JSON.parse(row.manifest) as PluginManifest;
	return {
		id: row.id,
		pluginId: row.pluginId,
		version: row.version,
		manifest: parsedManifest,
		raw: row.manifest,
		manifestDigest: row.manifestDigest,
		artifactHash: row.artifactHash ?? null,
		artifactSizeBytes: row.artifactSizeBytes ?? null,
		approvalStatus: (row.approvalStatus as PluginRegistryStatus) ?? 'pending',
		publishedAt: toDateValue(row.publishedAt) ?? new Date(),
		publishedBy: row.publishedBy ?? null,
		approvedAt: toDateValue(row.approvedAt),
		approvedBy: row.approvedBy ?? null,
		approvalNote: row.approvalNote ?? null,
		revokedAt: toDateValue(row.revokedAt),
		revokedBy: row.revokedBy ?? null,
		revocationReason: row.revocationReason ?? null,
		metadata: parseMetadata(row.metadata),
		createdAt: toDateValue(row.createdAt) ?? new Date(),
		updatedAt: toDateValue(row.updatedAt) ?? new Date()
	};
};

export interface PluginRegistryStore {
	publish(input: PublishPluginInput): Promise<PluginRegistryRecord>;
	approve(input: ApprovePluginInput): Promise<PluginRegistryRecord>;
	revoke(input: RevokePluginInput): Promise<PluginRegistryRecord>;
	list(): Promise<PluginRegistryRecord[]>;
	getById(id: string): Promise<PluginRegistryRecord | null>;
	getLatest(pluginId: string): Promise<PluginRegistryRecord | null>;
}

export const createPluginRegistryStore = (
	runtimeStore: PluginRuntimeStore = createPluginRuntimeStore()
): PluginRegistryStore => {
	const ensureDependenciesApproved = async (manifest: PluginManifest) => {
		const dependencies = manifestDependencies(manifest);
		if (!dependencies || dependencies.length === 0) {
			return;
		}

		const missing: string[] = [];
		for (const dependency of dependencies) {
			const runtime = await runtimeStore.find(dependency);
			if (!runtime) {
				missing.push(`${dependency} (not registered)`);
				continue;
			}
			if (runtime.approvalStatus !== 'approved') {
				missing.push(`${dependency} (approval ${runtime.approvalStatus})`);
				continue;
			}
			if (runtime.signatureStatus !== 'trusted') {
				missing.push(`${dependency} (signature ${runtime.signatureStatus})`);
			}
		}

		if (missing.length > 0) {
			throw new PluginRegistryError(
				`Plugin dependencies for ${manifest.id} are not satisfied: ${missing.join(', ')}`
			);
		}
	};

	const verifyManifest = async (
		manifest: PluginManifest,
		preverifiedSummary?: PluginSignatureVerificationSummary | null
	): Promise<PluginSignatureVerificationSummary> => {
		if (preverifiedSummary) {
			return preverifiedSummary;
		}
		try {
			const result = await verifyPluginSignature(manifest, verificationOptions());
			return summarizeVerificationSuccess(manifest, result);
		} catch (error) {
			const err = error as PluginSignatureVerificationError | Error;
			return summarizeVerificationFailure(manifest, err);
		}
	};

	const publish = async (input: PublishPluginInput): Promise<PluginRegistryRecord> => {
		const manifest = input.manifest;
		if (!input.skipValidation) {
			const validationErrors = validatePluginManifest(manifest);
			if (validationErrors.length > 0) {
				throw new PluginRegistryError(
					`Plugin manifest failed validation: ${validationErrors.join(', ')}`
				);
			}
		}

		await ensureDependenciesApproved(manifest);

		const pluginId = manifest.id.trim();
		if (!pluginId) {
			throw new PluginRegistryError('Plugin manifest is missing id');
		}

		const version = manifest.version.trim();
		if (!version) {
			throw new PluginRegistryError('Plugin manifest is missing version');
		}

		const manifestJson = JSON.stringify(manifest);
		const digest = computeManifestDigest(manifestJson);
		const artifactHash = manifest.package?.hash?.trim().toLowerCase() ?? null;
		const artifactSize = manifest.package?.sizeBytes ?? null;

		const metadata = serializeMetadata(input.metadata);

		const existing = await db
			.select({ id: registryTable.id })
			.from(registryTable)
			.where(and(eq(registryTable.pluginId, pluginId), eq(registryTable.version, version)))
			.limit(1);

		if (existing.length > 0) {
			throw new PluginRegistryError(`Plugin ${pluginId} version ${version} is already published`);
		}

		const now = new Date();
		const id = randomUUID();

		await db
			.insert(registryTable)
			.values({
				id,
				pluginId,
				version,
				manifest: manifestJson,
				manifestDigest: digest,
				artifactHash,
				artifactSizeBytes: artifactSize ?? null,
				metadata,
				approvalStatus: 'pending',
				publishedBy: input.actorId ?? null,
				publishedAt: now,
				approvalNote: input.approvalNote ?? null,
				createdAt: now,
				updatedAt: now
			})
			.run();

		const verification = await verifyManifest(manifest, input.preverifiedSummary ?? null);
		const loadedRecord = {
			source: `registry:${id}`,
			manifest,
			verification,
			raw: manifestJson
		} satisfies Parameters<PluginRuntimeStore['ensure']>[0];

		await runtimeStore.ensure(loadedRecord);
		await runtimeStore.update(pluginId, {
			approvalStatus: 'pending',
			approvedAt: null,
			approvalNote: input.approvalNote ?? null
		});

		const [row] = await db.select().from(registryTable).where(eq(registryTable.id, id)).limit(1);

		if (!row) {
			throw new PluginRegistryError('Failed to persist registry entry');
		}

		return toRegistryRecord(row);
	};

	const approve = async (input: ApprovePluginInput): Promise<PluginRegistryRecord> => {
		const [row] = await db
			.select()
			.from(registryTable)
			.where(eq(registryTable.id, input.id))
			.limit(1);

		if (!row) {
			throw new PluginRegistryError('Registry entry not found');
		}

		const updatedAt = new Date();
		const approvedAt = new Date();

		await db
			.update(registryTable)
			.set({
				approvalStatus: 'approved',
				approvedAt,
				approvedBy: input.actorId,
				approvalNote: input.note ?? row.approvalNote ?? null,
				revokedAt: null,
				revokedBy: null,
				revocationReason: null,
				updatedAt
			})
			.where(eq(registryTable.id, input.id));

		await runtimeStore.update(row.pluginId, {
			approvalStatus: 'approved',
			approvedAt,
			approvalNote: input.note ?? row.approvalNote ?? null
		});

		const [next] = await db
			.select()
			.from(registryTable)
			.where(eq(registryTable.id, input.id))
			.limit(1);

		if (!next) {
			throw new PluginRegistryError('Failed to load updated registry entry');
		}

		return toRegistryRecord(next);
	};

	const revoke = async (input: RevokePluginInput): Promise<PluginRegistryRecord> => {
		const [row] = await db
			.select()
			.from(registryTable)
			.where(eq(registryTable.id, input.id))
			.limit(1);

		if (!row) {
			throw new PluginRegistryError('Registry entry not found');
		}

		const revokedAt = new Date();
		const updatedAt = new Date();

		await db
			.update(registryTable)
			.set({
				approvalStatus: 'rejected',
				revokedAt,
				revokedBy: input.actorId,
				revocationReason: input.reason ?? null,
				updatedAt
			})
			.where(eq(registryTable.id, input.id));

		await runtimeStore.update(row.pluginId, {
			approvalStatus: 'rejected',
			approvedAt: null,
			approvalNote: input.reason ?? row.approvalNote ?? null
		});

		const [next] = await db
			.select()
			.from(registryTable)
			.where(eq(registryTable.id, input.id))
			.limit(1);

		if (!next) {
			throw new PluginRegistryError('Failed to load updated registry entry');
		}

		return toRegistryRecord(next);
	};

	const list = async (): Promise<PluginRegistryRecord[]> => {
		const rows = await db
			.select()
			.from(registryTable)
			.orderBy(desc(registryTable.publishedAt), desc(registryTable.createdAt));
		return rows.map(toRegistryRecord);
	};

	const getById = async (id: string): Promise<PluginRegistryRecord | null> => {
		const trimmed = id.trim();
		if (!trimmed) {
			return null;
		}
		const [row] = await db
			.select()
			.from(registryTable)
			.where(eq(registryTable.id, trimmed))
			.limit(1);
		return row ? toRegistryRecord(row) : null;
	};

	const getLatest = async (pluginId: string): Promise<PluginRegistryRecord | null> => {
		const trimmed = pluginId.trim();
		if (!trimmed) {
			return null;
		}
		const [row] = await db
			.select()
			.from(registryTable)
			.where(eq(registryTable.pluginId, trimmed))
			.orderBy(desc(registryTable.publishedAt), desc(registryTable.createdAt))
			.limit(1);
		return row ? toRegistryRecord(row) : null;
	};

	return { publish, approve, revoke, list, getById, getLatest };
};
