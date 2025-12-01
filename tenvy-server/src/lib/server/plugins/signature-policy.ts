import { env } from '$env/dynamic/private';
import { existsSync, readFileSync } from 'node:fs';
import { isAbsolute } from 'node:path';
import type { AgentPluginSignaturePolicy } from '../../../../../shared/types/config';
import type { PluginSignatureVerificationOptions } from '../../../../../shared/types/plugin-manifest';
import { resolveProjectPath } from '$lib/server/path-utils.js';

type ServerSignaturePolicy = {
	sha256AllowList: string[];
	ed25519PublicKeys: Record<string, string>;
	maxSignatureAgeMs?: number;
};

const DEFAULT_POLICY: ServerSignaturePolicy = {
	sha256AllowList: [],
	ed25519PublicKeys: {},
	maxSignatureAgeMs: undefined
};

let cachedPolicy: ServerSignaturePolicy | null = null;

const resolveRootRelativePath = (value: string): string =>
	isAbsolute(value) ? value : resolveProjectPath(value);

const policyPath = (): string => {
	const override = env.TENVY_PLUGIN_TRUST_CONFIG?.trim();
	if (override && override.length > 0) {
		return resolveRootRelativePath(override);
	}
	return resolveProjectPath('resources', 'plugin-signers.json');
};

const normalizeHex = (value: string | undefined | null): string =>
	value?.trim().toLowerCase() ?? '';

const parsePolicy = (raw: unknown): ServerSignaturePolicy => {
	if (!raw || typeof raw !== 'object') {
		return { ...DEFAULT_POLICY };
	}

	const input = raw as Record<string, unknown>;
	const policy: ServerSignaturePolicy = {
		sha256AllowList: [],
		ed25519PublicKeys: {},
		maxSignatureAgeMs: undefined
	};

	if (input.allowUnsigned) {
		console.warn('Ignoring deprecated allowUnsigned flag in plugin signature policy');
	}

	if (Array.isArray(input.sha256AllowList)) {
		policy.sha256AllowList = input.sha256AllowList
			.map((value) => (typeof value === 'string' ? normalizeHex(value) : ''))
			.filter((value) => value.length > 0);
	}

	const keyMap = input.ed25519PublicKeys;
	if (keyMap && typeof keyMap === 'object' && !Array.isArray(keyMap)) {
		for (const [keyId, value] of Object.entries(keyMap as Record<string, unknown>)) {
			if (typeof value !== 'string') {
				console.warn(`Ignoring non-string Ed25519 key for signer ${keyId}`);
				continue;
			}
			const trimmed = normalizeHex(value);
			if (trimmed.length !== 64) {
				console.warn(
					`Ignoring Ed25519 key for signer ${keyId}: expected 32-byte hex, received ${value.length} characters`
				);
				continue;
			}
			policy.ed25519PublicKeys[keyId] = trimmed;
		}
	}

	const maxAge = input.maxSignatureAgeMs;
	if (typeof maxAge === 'number' && Number.isFinite(maxAge) && maxAge > 0) {
		policy.maxSignatureAgeMs = Math.floor(maxAge);
	}

	return policy;
};

const loadPolicyFromDisk = (): ServerSignaturePolicy => {
	const path = policyPath();
	if (!existsSync(path)) {
		return { ...DEFAULT_POLICY };
	}

	try {
		const raw = readFileSync(path, 'utf8');
		if (raw.trim().length === 0) {
			return { ...DEFAULT_POLICY };
		}
		const parsed = JSON.parse(raw) as unknown;
		return parsePolicy(parsed);
	} catch (error) {
		console.error(`Failed to load plugin signature policy at ${path}:`, error);
		return { ...DEFAULT_POLICY };
	}
};

export const refreshSignaturePolicy = (): void => {
	cachedPolicy = loadPolicyFromDisk();
};

export const getSignaturePolicy = (): ServerSignaturePolicy => {
	if (!cachedPolicy) {
		cachedPolicy = loadPolicyFromDisk();
	}
	return cachedPolicy;
};

const toUint8Array = (hex: string): Uint8Array => {
	const bytes = new Uint8Array(hex.length / 2);
	for (let i = 0; i < hex.length; i += 2) {
		const byte = Number.parseInt(hex.slice(i, i + 2), 16);
		if (Number.isNaN(byte)) {
			throw new Error('value contains non-hex characters');
		}
		bytes[i / 2] = byte;
	}
	return bytes;
};

const ed25519KeyBytes = (policy: ServerSignaturePolicy): Record<string, Uint8Array> => {
	const entries: [string, Uint8Array][] = [];
	for (const [keyId, value] of Object.entries(policy.ed25519PublicKeys)) {
		try {
			entries.push([keyId, toUint8Array(value)]);
		} catch (error) {
			console.warn(`Failed to parse Ed25519 key for signer ${keyId}:`, error);
		}
	}
	return Object.fromEntries(entries);
};

export const getVerificationOptions = (): PluginSignatureVerificationOptions => {
	const policy = getSignaturePolicy();
	const options: PluginSignatureVerificationOptions = {
		sha256AllowList: policy.sha256AllowList,
		ed25519PublicKeys: ed25519KeyBytes(policy)
	};
	if (policy.maxSignatureAgeMs && policy.maxSignatureAgeMs > 0) {
		options.maxSignatureAgeMs = policy.maxSignatureAgeMs;
	}
	return options;
};

export const getAgentSignaturePolicy = (): AgentPluginSignaturePolicy => {
	const policy = getSignaturePolicy();
	const agentPolicy: AgentPluginSignaturePolicy = {
		sha256AllowList: policy.sha256AllowList,
		ed25519PublicKeys: policy.ed25519PublicKeys
	};
	if (policy.maxSignatureAgeMs && policy.maxSignatureAgeMs > 0) {
		agentPolicy.maxSignatureAgeMs = policy.maxSignatureAgeMs;
	}
	return agentPolicy;
};
