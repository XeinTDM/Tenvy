import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { registry, RegistryError } from '$lib/server/rat/store';
import { requireOperator, requireViewer } from '$lib/server/authorization';
import { sanitizeAcknowledgement } from '$lib/server/rat/utils';
import type {
	CommandAcknowledgementRecord,
	CommandInput,
	CommandQueueSnapshot
} from '../../../../../../../shared/types/messages';

export const POST: RequestHandler = async ({ params, request, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	const user = requireOperator(locals.user);

	let body: unknown;
	try {
		body = await request.json();
	} catch {
		throw error(400, 'Invalid command payload');
	}

	if (!body || typeof body !== 'object') {
		throw error(400, 'Invalid command payload');
	}

	const { acknowledgement: acknowledgementRaw, ...rest } = body as Record<string, unknown>;

	const name = typeof rest.name === 'string' ? rest.name : '';
	if (!name) {
		throw error(400, 'Command name is required');
	}

	const commandInput: CommandInput = {
		name: name as CommandInput['name'],
		payload: rest.payload as CommandInput['payload']
	};

	let acknowledgement = sanitizeAcknowledgement(acknowledgementRaw as CommandAcknowledgementRecord);

	if (commandInput.name === 'open-url' && !acknowledgement) {
		throw error(400, 'Open URL requests require acknowledgement');
	}

	if (commandInput.name !== 'open-url') {
		acknowledgement = null;
	}

	try {
		const response = registry.queueCommand(id, commandInput, {
			operatorId: user.id,
			acknowledgement
		});
		return json(response);
	} catch (err) {
		if (err instanceof RegistryError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to queue command');
	}
};

export const GET: RequestHandler = ({ params, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	requireViewer(locals.user);

	try {
		const commands = registry.peekCommands(id);
		return json({ commands } satisfies CommandQueueSnapshot);
	} catch (err) {
		if (err instanceof RegistryError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to fetch queued commands');
	}
};
