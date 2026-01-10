import { agentModuleCapabilityIndex, agentModuleIndex } from '../../../../shared/modules/index.js';
import type {
	PluginManifest,
	PluginRuntimeType,
	PluginSignatureStatus,
	PluginSignatureType
} from '../../../../shared/types/plugin-manifest';
import { loadPluginManifests, type LoadedPluginManifest } from './plugin-manifests.js';
import {
	formatFileSize,
	formatRelativeTime,
	pluginCategories,
	pluginCategoryLabels,
	pluginDeliveryModeLabels,
	pluginStatusLabels,
	pluginStatusStyles,
	type Plugin,
	type PluginCategory,
	type PluginDeliveryMode,
	type PluginStatus,
	type PluginUpdatePayload,
	type PluginApprovalStatus
} from './plugin-view.js';
import {
	createPluginRuntimeStore,
	type PluginRuntimePatch,
	type PluginRuntimeRow,
	type PluginRuntimeStore
} from '$lib/server/plugins/runtime-store.js';

export type {
	Plugin,
	PluginRuntimeSummary,
	PluginCategory,
	PluginDeliveryMode,
	PluginDistributionView,
	PluginStatus,
	PluginUpdatePayload
} from './plugin-view.js';
export {
	formatFileSize,
	formatRelativeTime,
	pluginCategories,
	pluginCategoryLabels,
	pluginDeliveryModeLabels,
	pluginStatusLabels,
	pluginStatusStyles
};

export interface PluginRepositoryOptions {
	directory?: string;
	runtimeStore?: PluginRuntimeStore;
}

export type PluginRepositoryUpdate = PluginUpdatePayload & {
	distribution?: PluginUpdatePayload['distribution'] & {
		manualTargets?: number;
		autoTargets?: number;
		lastManualPushAt?: Date | null;
		lastAutoSyncAt?: Date | null;
	};
	lastDeployedAt?: Date | null;
	lastCheckedAt?: Date | null;
	approvalStatus?: PluginApprovalStatus;
	approvedAt?: Date | null;
	approvalNote?: string | null;
};

type PluginRuntimeSnapshot = {
	status: PluginStatus;
	enabled: boolean;
	autoUpdate: boolean;
	runtimeType: PluginRuntimeType;
	sandboxed: boolean;
	installations: number;
	manualTargets: number;
	autoTargets: number;
	defaultDeliveryMode: PluginDeliveryMode;
	allowManualPush: boolean;
	allowAutoSync: boolean;
	lastManualPushAt: Date | null;
	lastAutoSyncAt: Date | null;
	lastDeployedAt: Date | null;
	lastCheckedAt: Date | null;
	approvalStatus: PluginApprovalStatus;
	approvedAt: Date | null;
	approvalNote: string | null;
	signature: {
		status: PluginSignatureStatus;
		trusted: boolean;
		type: PluginSignatureType;
		hash: string | null;
		signer: string | null;
		publicKey: string | null;
		checkedAt: Date | null;
		signedAt: Date | null;
		error: string | null;
		errorCode: string | null;
		certificateChain: string[] | null;
	};
};

const manifestCategory = (manifest: PluginManifest): PluginCategory => {
	const category = manifest.categories?.[0];
	if (!category) return 'operations';
	return (category as PluginCategory) ?? 'operations';
};

const mapRequiredModules = (manifest: PluginManifest) =>
	(manifest.requirements.requiredModules ?? [])
		.map((moduleId) => agentModuleIndex.get(moduleId))
		.filter((module): module is NonNullable<typeof module> => module != null)
		.map((module) => ({ id: module.id, title: module.title }));

const mapCapabilities = (manifest: PluginManifest): string[] =>
	(manifest.capabilities ?? []).map((capabilityId) => {
		const capability = agentModuleCapabilityIndex.get(capabilityId);
		return capability?.name ?? capabilityId;
	});

const normalizeDependencies = (values: readonly string[] | undefined): string[] => {
	if (!values || values.length === 0) {
		return [];
	}
	const unique = new Set<string>();
	const normalized: string[] = [];
	for (const value of values) {
		const trimmed = value?.trim();
		if (!trimmed) {
			continue;
		}
		const lowered = trimmed.toLowerCase();
		if (unique.has(lowered)) {
			continue;
		}
		unique.add(lowered);
		normalized.push(trimmed);
	}
	return normalized;
};

const toPluginView = (record: LoadedPluginManifest, runtime: PluginRuntimeSnapshot): Plugin => ({
	id: record.manifest.id,
	name: record.manifest.name,
	description: record.manifest.description ?? '',
	version: record.manifest.version,
	author: record.manifest.author ?? 'Unknown',
	category: manifestCategory(record.manifest),
	status: runtime.status,
	enabled: runtime.enabled,
	autoUpdate: runtime.autoUpdate,
	installations: runtime.installations,
	lastDeployed: formatRelativeTime(runtime.lastDeployedAt),
	lastChecked: formatRelativeTime(runtime.lastCheckedAt),
	size: formatFileSize(record.manifest.package.sizeBytes),
	capabilities: mapCapabilities(record.manifest),
	artifact: record.manifest.package.artifact,
	runtime: {
		type: runtime.runtimeType,
		sandboxed: runtime.sandboxed
	},
	distribution: {
		defaultMode: runtime.defaultDeliveryMode,
		allowManualPush: runtime.allowManualPush,
		allowAutoSync: runtime.allowAutoSync,
		manualTargets: runtime.manualTargets,
		autoTargets: runtime.autoTargets,
		lastManualPush: formatRelativeTime(runtime.lastManualPushAt),
		lastAutoSync: formatRelativeTime(runtime.lastAutoSyncAt)
	},
	dependencies: normalizeDependencies(record.manifest.dependencies),
	requiredModules: mapRequiredModules(record.manifest),
	approvalStatus: runtime.approvalStatus,
	approvedAt: runtime.approvedAt ? runtime.approvedAt.toISOString() : undefined,
	signature: {
		status: runtime.signature.status,
		trusted: runtime.signature.trusted,
		type: runtime.signature.type,
		hash: runtime.signature.hash,
		signer: runtime.signature.signer,
		publicKey: runtime.signature.publicKey,
		signedAt: runtime.signature.signedAt ? runtime.signature.signedAt.toISOString() : null,
		checkedAt: runtime.signature.checkedAt ? runtime.signature.checkedAt.toISOString() : null,
		error: runtime.signature.error,
		errorCode: runtime.signature.errorCode,
		certificateChain: runtime.signature.certificateChain
	}
});

const toRuntimePatch = (update: PluginRepositoryUpdate): PluginRuntimePatch => {
	const patch: PluginRuntimePatch = {};

	if (update.status !== undefined) patch.status = update.status;
	if (update.enabled !== undefined) patch.enabled = update.enabled;
	if (update.autoUpdate !== undefined) patch.autoUpdate = update.autoUpdate;
	if (update.installations !== undefined) patch.installations = update.installations;
	if (update.lastDeployedAt !== undefined) patch.lastDeployedAt = update.lastDeployedAt;
	if (update.lastCheckedAt !== undefined) patch.lastCheckedAt = update.lastCheckedAt;
	if (update.approvalStatus !== undefined) patch.approvalStatus = update.approvalStatus;
	if (update.approvedAt !== undefined) patch.approvedAt = update.approvedAt;
	if (update.approvalNote !== undefined) patch.approvalNote = update.approvalNote;

	if (update.distribution) {
		const { distribution } = update;
		if (distribution.defaultMode !== undefined)
			patch.defaultDeliveryMode = distribution.defaultMode;
		if (distribution.allowManualPush !== undefined)
			patch.allowManualPush = distribution.allowManualPush;
		if (distribution.allowAutoSync !== undefined) patch.allowAutoSync = distribution.allowAutoSync;
		if (distribution.manualTargets !== undefined) patch.manualTargets = distribution.manualTargets;
		if (distribution.autoTargets !== undefined) patch.autoTargets = distribution.autoTargets;
		if (distribution.lastManualPushAt !== undefined)
			patch.lastManualPushAt = distribution.lastManualPushAt;
		if (distribution.lastAutoSyncAt !== undefined)
			patch.lastAutoSyncAt = distribution.lastAutoSyncAt;
	}

	return patch;
};

const snapshotFromRow = (row: PluginRuntimeRow): PluginRuntimeSnapshot => ({
	status: row.status as PluginStatus,
	enabled: row.enabled,
	autoUpdate: row.autoUpdate,
	runtimeType: row.runtimeType as PluginRuntimeType,
	sandboxed: row.sandboxed,
	installations: row.installations,
	manualTargets: row.manualTargets,
	autoTargets: row.autoTargets,
	defaultDeliveryMode: row.defaultDeliveryMode as PluginDeliveryMode,
	allowManualPush: row.allowManualPush,
	allowAutoSync: row.allowAutoSync,
	lastManualPushAt: row.lastManualPushAt ?? null,
	lastAutoSyncAt: row.lastAutoSyncAt ?? null,
	lastDeployedAt: row.lastDeployedAt ?? null,
	lastCheckedAt: row.lastCheckedAt ?? null,
	approvalStatus: row.approvalStatus as PluginApprovalStatus,
	approvedAt: row.approvedAt ?? null,
	approvalNote: row.approvalNote ?? null,
	signature: {
		status: row.signatureStatus as PluginSignatureStatus,
		trusted: Boolean(row.signatureTrusted),
		type: row.signatureType as PluginSignatureType,
		hash: row.signatureHash ?? null,
		signer: row.signatureSigner ?? null,
		publicKey: row.signaturePublicKey ?? null,
		checkedAt: row.signatureCheckedAt ?? null,
		signedAt: row.signatureSignedAt ?? null,
		error: row.signatureError ?? null,
		errorCode: row.signatureErrorCode ?? null,
		certificateChain: (() => {
			if (!row.signatureChain) return null;
			try {
				const parsed = JSON.parse(row.signatureChain);
				return Array.isArray(parsed)
					? parsed.filter((value): value is string => typeof value === 'string')
					: null;
			} catch {
				return null;
			}
		})()
	}
});

export const createPluginRepository = (
	options: PluginRepositoryOptions = {}
): {
	list(): Promise<Plugin[]>;
	get(id: string): Promise<Plugin>;
	update(id: string, update: PluginRepositoryUpdate): Promise<Plugin>;
} => {
	const runtimeStore = options.runtimeStore ?? createPluginRuntimeStore();

	const loadManifests = async () => loadPluginManifests({ directory: options.directory });

	const manifestIndex = async () => {
		const records = await loadManifests();
		const index = new Map(records.map((record) => [record.manifest.id, record]));
		return { records, index };
	};

	const getManifest = async (id: string): Promise<LoadedPluginManifest> => {
		const { index } = await manifestIndex();
		const record = index.get(id);
		if (!record) throw new Error(`Plugin manifest ${id} not found`);
		return record;
	};

	return {
		async list() {
			const records = await loadManifests();
			if (records.length === 0) return [];

			const runtimeRows = await Promise.all(records.map((record) => runtimeStore.ensure(record)));

			return records.map((record, index) =>
				toPluginView(record, snapshotFromRow(runtimeRows[index]!))
			);
		},
		async get(id: string) {
			const record = await getManifest(id);
			const runtimeRow = await runtimeStore.ensure(record);
			return toPluginView(record, snapshotFromRow(runtimeRow));
		},
		async update(id: string, update) {
			const record = await getManifest(id);
			await runtimeStore.ensure(record);

			const runtimeRow = await runtimeStore.update(id, toRuntimePatch(update));
			return toPluginView(record, snapshotFromRow(runtimeRow));
		}
	};
};
