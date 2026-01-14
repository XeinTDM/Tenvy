import { createHash, randomBytes, timingSafeEqual } from 'crypto';
import type { AgentMetadata } from '../../../../../shared/types/agent';
import type { AgentConfig, AgentPluginConfig, AgentPluginSignaturePolicy } from '../../../../../shared/types/config';
import { defaultAgentConfig } from '../../../../../shared/types/config';
import type { OptionsState, OptionsScriptConfig, OptionsScriptFile, OptionsScriptRuntimeState } from '../../../../../shared/types/options';
import type { Command, CommandResult, CommandOutputEvent } from '../../../../../shared/types/messages';
import { downloadCatalogueSchema, type DownloadCatalogue, type DownloadCatalogueEntry } from '$lib/types/downloads';
import { getAgentSignaturePolicy } from '../plugins/signature-policy';

export const MAX_TAGS = 16;
export const MAX_TAG_LENGTH = 32;
export const TAG_PATTERN = /^[\p{L}\p{N}_\-\s]+$/u;

export const MAX_RECENT_RESULTS = 25;
export const MAX_PENDING_COMMANDS = 200;
export const PENDING_COMMAND_DROP_WARN_INTERVAL_MS = 30_000;
export const PERSIST_DEBOUNCE_MS = 2_000;
export const SESSION_TOKEN_TTL_MS = 60_000;
export const COMMAND_OUTPUT_RETENTION_MS = 5 * 60 * 1000;
export const INACTIVITY_CHECK_INTERVAL_MS = 15_000;
export const INACTIVITY_TIMEOUT_MULTIPLIER = 2;
export const MIN_INACTIVITY_TIMEOUT_MS = 15_000;

export function ensureMetadata(metadata: AgentMetadata, remoteAddress?: string): AgentMetadata {
	if (!remoteAddress) {
		return metadata;
	}

	const next: AgentMetadata = { ...metadata };

	if (!next.ipAddress) {
		next.ipAddress = remoteAddress;
	}

	if (!next.publicIpAddress || next.publicIpAddress.trim() === '') {
		next.publicIpAddress = remoteAddress;
	}

	return next;
}

export function computeFingerprint(metadata: AgentMetadata): string {
	const normalize = (value: string | undefined) => value?.trim().toLowerCase() ?? '';
	const hash = createHash('sha256');
	hash.update(normalize(metadata.hostname));
	hash.update('|');
	hash.update(normalize(metadata.username));
	hash.update('|');
	hash.update(normalize(metadata.os));
	hash.update('|');
	hash.update(normalize(metadata.architecture));
	hash.update('|');
	hash.update(normalize(metadata.group));
	hash.update('|');
	hash.update(normalize(metadata.hardwareId));
	return hash.digest('hex');
}

export function hashAgentKey(rawKey: string): string {
	const hash = createHash('sha256');
	hash.update(rawKey, 'utf-8');
	return hash.digest('hex');
}

export function hashSessionToken(rawToken: string): string {
	const hash = createHash('sha256');
	hash.update(rawToken, 'utf-8');
	return hash.digest('hex');
}

export function timingSafeEqualHex(expected: string, candidate: string): boolean {
	if (expected.length !== candidate.length) {
		return false;
	}

	try {
		const expectedBuffer = Buffer.from(expected, 'hex');
		const candidateBuffer = Buffer.from(candidate, 'hex');
		return timingSafeEqual(expectedBuffer, candidateBuffer);
	} catch {
		return false;
	}
}

export function generateAgentKey(): { token: string; hash: string } {
	const token = randomBytes(32).toString('hex');
	return { token, hash: hashAgentKey(token) };
}

export function generateSessionToken(ttlMs: number): { token: string; hash: string; expiresAt: number } {
	const token = randomBytes(32).toString('hex');
	return { token, hash: hashSessionToken(token), expiresAt: Date.now() + ttlMs };
}

export function parseNumeric(value: unknown): number | null {
	if (typeof value === 'number') {
		return Number.isFinite(value) ? value : null;
	}
	if (typeof value === 'string' && value.trim() !== '') {
		const parsed = Number(value);
		return Number.isFinite(parsed) ? parsed : null;
	}
	return null;
}

export function cloneSignaturePolicy(
	policy: AgentPluginSignaturePolicy | undefined
): AgentPluginSignaturePolicy | undefined {
	if (!policy) {
		return undefined;
	}

	const cloned: AgentPluginSignaturePolicy = { ...policy };

	if (Array.isArray(policy.sha256AllowList)) {
		cloned.sha256AllowList = [...policy.sha256AllowList];
	}

	if (policy.ed25519PublicKeys) {
		cloned.ed25519PublicKeys = { ...policy.ed25519PublicKeys };
	}

	return cloned;
}

export function clonePluginConfig(config?: AgentPluginConfig | null): AgentPluginConfig | undefined {
	if (!config || typeof config !== 'object') {
		return undefined;
	}

	const clone: AgentPluginConfig = {};

	for (const [key, value] of Object.entries(config)) {
		if (key === 'signaturePolicy') {
			continue;
		}
		(clone as Record<string, unknown>)[key] = value;
	}

	return clone;
}

export function normalizeConfig(config?: Partial<AgentConfig> | null): AgentConfig {
	const normalized: AgentConfig = {
		...defaultAgentConfig
	};

	if (!config) {
		return normalized;
	}

	const pollInterval = parseNumeric(config.pollIntervalMs);
	if (pollInterval !== null && pollInterval > 0) {
		normalized.pollIntervalMs = Math.max(1, Math.round(pollInterval));
	}

	const maxBackoff = parseNumeric(config.maxBackoffMs);
	if (maxBackoff !== null && maxBackoff > 0) {
		normalized.maxBackoffMs = Math.max(normalized.pollIntervalMs, Math.round(maxBackoff));
	}

	const jitter = parseNumeric(config.jitterRatio);
	if (jitter !== null && jitter >= 0 && jitter <= 1) {
		normalized.jitterRatio = jitter;
	}

	const pluginConfig = clonePluginConfig(config?.plugins);
	const signaturePolicy = cloneSignaturePolicy(getAgentSignaturePolicy());

	const mergedPluginConfig: AgentPluginConfig = {
		...(pluginConfig ?? {})
	};

	if (signaturePolicy) {
		mergedPluginConfig.signaturePolicy = signaturePolicy;
	}

	if (Object.keys(mergedPluginConfig).length > 0) {
		normalized.plugins = mergedPluginConfig;
	}

	return normalized;
}

export function cloneOptionsFile(
	file: OptionsScriptFile | null | undefined
): OptionsScriptFile | null | undefined {
	if (file === null || file === undefined) {
		return file ?? undefined;
	}
	return { ...file } satisfies OptionsScriptFile;
}

export function cloneOptionsConfig(
	config: OptionsScriptConfig | null | undefined
): OptionsScriptConfig | null | undefined {
	if (config === null || config === undefined) {
		return config ?? undefined;
	}
	const clone: OptionsScriptConfig = { ...config };
	if (config.file === null) {
		clone.file = null;
	} else if (config.file !== undefined) {
		clone.file = cloneOptionsFile(config.file) ?? undefined;
	}
	return clone;
}

export function cloneOptionsRuntime(
	runtime: OptionsScriptRuntimeState | null | undefined
): OptionsScriptRuntimeState | null | undefined {
	if (runtime === null || runtime === undefined) {
		return runtime ?? undefined;
	}
	return { ...runtime } satisfies OptionsScriptRuntimeState;
}

export function cloneOptionsState(state: OptionsState | null | undefined): OptionsState | null {
	if (state === null || state === undefined) {
		return state ?? null;
	}
	const clone: OptionsState = { ...state };
	if (state.script === null) {
		clone.script = null;
	} else if (state.script !== undefined) {
		clone.script = cloneOptionsConfig(state.script) ?? undefined;
	}
	if (state.scriptRuntime === null) {
		clone.scriptRuntime = null;
	} else if (state.scriptRuntime !== undefined) {
		clone.scriptRuntime = cloneOptionsRuntime(state.scriptRuntime) ?? undefined;
	}
	return clone;
}

export function normalizeCommandOutputEvent(
	commandId: string,
	event: CommandOutputEvent
): CommandOutputEvent {
	const timestamp =
		typeof event.timestamp === 'string' && event.timestamp.trim() !== ''
			? event.timestamp
			: new Date().toISOString();

	if (event.type === 'chunk') {
		const sequence = Number.isFinite(event.sequence) ? Number(event.sequence) : 0;
		return {
			type: 'chunk',
			commandId,
			sequence,
			data: typeof event.data === 'string' ? event.data : '',
			timestamp
		} satisfies CommandOutputEvent;
	}

	const baseResult = event.result ?? {
		commandId,
		success: false,
		output: undefined,
		error: 'Command result unavailable',
		completedAt: timestamp
	};

	const completedAt =
		typeof baseResult.completedAt === 'string' && baseResult.completedAt.trim() !== ''
			? baseResult.completedAt
			: timestamp;

	const normalizedCommandId =
		typeof baseResult.commandId === 'string' && baseResult.commandId.trim() !== ''
			? baseResult.commandId
			: commandId;

	return {
		type: 'end',
		commandId,
		timestamp,
		result: {
			commandId: normalizedCommandId,
			success: Boolean(baseResult.success),
			output: baseResult.output ?? undefined,
			error: baseResult.error ?? undefined,
			completedAt
		}
	} satisfies CommandOutputEvent;
}

export function parseCompletedAt(result: CommandResult | undefined): number {
	if (!result) {
		return 0;
	}
	if (typeof result.completedAt === 'string') {
		const parsed = Date.parse(result.completedAt);
		if (Number.isFinite(parsed)) {
			return parsed;
		}
	}
	return 0;
}

export function mergeRecentResults(existing: CommandResult[], incoming: CommandResult[], maxResults: number): CommandResult[] {
	if (existing.length === 0 && incoming.length === 0) {
		return [];
	}

	const merged = new Map<string, { result: CommandResult; timestamp: number }>();

	const upsert = (candidate: CommandResult | null | undefined) => {
		if (!candidate?.commandId) {
			return;
		}
		const timestamp = parseCompletedAt(candidate);
		const current = merged.get(candidate.commandId);
		if (!current || timestamp >= current.timestamp) {
			merged.set(candidate.commandId, {
				result: { ...candidate },
				timestamp
			});
		}
	};

	for (const result of existing) {
		upsert(result);
	}

	for (const result of incoming) {
		upsert(result);
	}

	return Array.from(merged.values())
		.sort((a, b) => {
			if (b.timestamp !== a.timestamp) {
				return b.timestamp - a.timestamp;
			}
			return b.result.commandId.localeCompare(a.result.commandId);
		})
		.slice(0, maxResults)
		.map((entry) => entry.result);
}

export function cloneDownloadEntry(entry: DownloadCatalogueEntry): DownloadCatalogueEntry {
	const clone: DownloadCatalogueEntry = { ...entry };
	if (Array.isArray(entry.tags)) {
		clone.tags = [...entry.tags];
	}
	return clone;
}

export function cloneDownloadCatalogue(
	entries: DownloadCatalogue | DownloadCatalogueEntry[] | null | undefined
): DownloadCatalogue {
	if (!entries || entries.length === 0) {
		return [];
	}
	return entries.map((entry) => cloneDownloadEntry(entry));
}

export function normalizeTags(tags: string[]): string[] {
	const seen = new Set<string>();
	const result: string[] = [];

	for (const entry of tags) {
		if (typeof entry !== 'string') {
			continue;
		}

		const trimmed = entry.trim();
		if (trimmed.length === 0 || trimmed.length > MAX_TAG_LENGTH) {
			continue;
		}

		if (!TAG_PATTERN.test(trimmed)) {
			continue;
		}

		const key = trimmed.toLowerCase();
		if (seen.has(key)) {
			continue;
		}

		seen.add(key);
		result.push(trimmed);

		if (result.length >= MAX_TAGS) {
			break;
		}
	}

	return result;
}