import { createHash, createHmac, randomBytes, timingSafeEqual, generateKeyPairSync, diffieHellman, createCipheriv, createDecipheriv } from 'crypto';
import { env } from '$env/dynamic/private';
import type { AgentMetadata } from '../../../../../shared/types/agent';
import type { AgentConfig, AgentPluginConfig, AgentPluginSignaturePolicy } from '../../../../../shared/types/config';
import { defaultAgentConfig } from '../../../../../shared/types/config';
import type { OptionsState, OptionsScriptConfig, OptionsScriptFile, OptionsScriptRuntimeState } from '../../../../../shared/types/options';
import type { Command, CommandResult, CommandOutputEvent, CommandAcknowledgementRecord } from '../../../../../shared/types/messages';
import { downloadCatalogueSchema, type DownloadCatalogue, type DownloadCatalogueEntry } from '$lib/types/downloads';
import { getAgentSignaturePolicy } from '../plugins/signature-policy';

export function generateX25519KeyPair(): { publicKey: string; privateKey: string } {
	const { publicKey, privateKey } = generateKeyPairSync('x25519');
	const pubDer = publicKey.export({ type: 'spki', format: 'der' });
	const privDer = privateKey.export({ type: 'pkcs8', format: 'der' });
	
	// X25519 SPKI header is 12 bytes: 30 2a 30 05 06 03 2b 65 6e 03 21 00
	const pubRaw = pubDer.subarray(12);
	
	// X25519 PKCS8 header is 16 bytes: 30 2e 02 01 00 30 05 06 03 2b 65 6e 04 22 04 20
	const privRaw = privDer.subarray(16);

	return {
		publicKey: pubRaw.toString('hex'),
		privateKey: privRaw.toString('hex')
	};
}

export function generateRawX25519KeyPair(): { publicKey: string; privateKey: string } {
	return generateX25519KeyPair();
}

export function deriveSharedSecret(privateKeyHex: string, peerPublicKeyHex: string): string {
	const privateKey = createPrivateKey({
		key: Buffer.from(privateKeyHex, 'hex'),
		format: 'der',
		type: 'pkcs8'
	});
	const publicKey = createPublicKey({
		key: Buffer.from(peerPublicKeyHex, 'hex'),
		format: 'der',
		type: 'spki'
	});
	return diffieHellman({ privateKey, publicKey }).toString('hex');
}

import { createPrivateKey, createPublicKey } from 'crypto';

export function deriveRawSharedSecret(privateKeyRawHex: string, peerPublicKeyRawHex: string): string {
	return deriveSharedSecretX25519(
		Buffer.from(privateKeyRawHex, 'hex'),
		Buffer.from(peerPublicKeyRawHex, 'hex')
	).toString('hex');
}

export function deriveSharedSecretX25519(privateKeyRaw: Buffer, peerPublicKeyRaw: Buffer): Buffer {
	const priv = createPrivateKey({
		key: Buffer.concat([
			Buffer.from('302e020100300506032b656e04220420', 'hex'),
			privateKeyRaw
		]),
		format: 'der',
		type: 'pkcs8'
	});
	const pub = createPublicKey({
		key: Buffer.concat([
			Buffer.from('302a300506032b656e032100', 'hex'),
			peerPublicKeyRaw
		]),
		format: 'der',
		type: 'spki'
	});
	
	return diffieHellman({ privateKey: priv, publicKey: pub });
}

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
	const secret = env.TENVY_SESSION_SECRET ?? env.TENVY_SHARED_SECRET ?? 'default-insecure-secret';
	const hmac = createHmac('sha256', secret);
	hmac.update(rawToken, 'utf-8');
	return hmac.digest('hex');
}

export function hashCommandPayload(payload: Command['payload']): string {
	const hash = createHash('sha256');
	try {
		const serialized = JSON.stringify(payload ?? {});
		hash.update(serialized, 'utf-8');
	} catch {
		hash.update('unserializable', 'utf-8');
	}
	return hash.digest('hex');
}

export function sanitizeAcknowledgement(
	input: CommandAcknowledgementRecord | null | undefined
): CommandAcknowledgementRecord | null {
	if (!input || typeof input !== 'object') {
		return null;
	}

	const rawTimestamp = typeof input.confirmedAt === 'string' ? input.confirmedAt.trim() : '';
	const statementsSource = Array.isArray(input.statements) ? input.statements : [];

	const statements = statementsSource
		.map((statement) => {
			if (!statement || typeof statement !== 'object') {
				return null;
			}
			const id =
				typeof (statement as { id?: unknown }).id === 'string'
					? (statement as { id: string }).id.trim()
					: '';
			const text =
				typeof (statement as { text?: unknown }).text === 'string'
					? (statement as { text: string }).text.trim()
					: '';
			if (!id || !text) {
				return null;
			}
			return { id, text };
		})
		.filter((entry): entry is { id: string; text: string } => Boolean(entry));

	if (statements.length === 0) {
		return null;
	}

	const parsedTimestamp = rawTimestamp ? new Date(rawTimestamp) : new Date();
	const confirmedAt = Number.isNaN(parsedTimestamp.getTime())
		? new Date().toISOString()
		: parsedTimestamp.toISOString();

	return { confirmedAt, statements };
}

export function deserializeAcknowledgement(value: string | null): CommandAcknowledgementRecord | null {
	if (!value) {
		return null;
	}

	try {
		const parsed = JSON.parse(value) as CommandAcknowledgementRecord;
		return sanitizeAcknowledgement(parsed);
	} catch {
		return null;
	}
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

export function encryptDatabaseField(value: string): string | null {
	const masterSecret = env.TENVY_COMMAND_SECRET || env.TENVY_SHARED_SECRET;
	if (!masterSecret || !value) return value;

	try {
		const key = createHash('sha256').update(masterSecret).digest();
		const iv = randomBytes(12);
		const cipher = createCipheriv('aes-256-gcm', key, iv);
		const encrypted = Buffer.concat([cipher.update(value, 'utf8'), cipher.final()]);
		const tag = cipher.getAuthTag();
		return Buffer.concat([iv, tag, encrypted]).toString('base64');
	} catch (err) {
		console.error('Failed to encrypt database field', err);
		return null;
	}
}

export function decryptDatabaseField(encryptedBase64: string): string | null {
	const masterSecret = env.TENVY_COMMAND_SECRET || env.TENVY_SHARED_SECRET;
	if (!masterSecret || !encryptedBase64) return encryptedBase64;

	try {
		const key = createHash('sha256').update(masterSecret).digest();
		const data = Buffer.from(encryptedBase64, 'base64');
		if (data.length < 28) return null;

		const iv = data.subarray(0, 12);
		const tag = data.subarray(12, 28);
		const ciphertext = data.subarray(28);

		const decipher = createDecipheriv('aes-256-gcm', key, iv);
		decipher.setAuthTag(tag);
		return Buffer.concat([decipher.update(ciphertext), decipher.final()]).toString('utf8');
	} catch (err) {
		return null;
	}
}