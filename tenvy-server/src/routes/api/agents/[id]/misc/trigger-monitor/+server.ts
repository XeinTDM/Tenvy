import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { requireOperator, requireViewer } from '$lib/server/authorization';
import {
	dispatchTriggerMonitorCommand,
	TriggerMonitorAgentError
} from '$lib/server/rat/trigger-monitor';
import {
	triggerMonitorCommandRequestSchema,
	triggerMonitorWatchlistInputSchema,
	type TriggerMonitorStatus
} from '$lib/types/trigger-monitor';
import { ZodError } from 'zod';

export const _MAX_TRIGGER_MONITOR_REQUEST_BYTES = 16 * 1024;

function formatValidationError(err: ZodError) {
	const issue = err.issues[0];
	if (!issue) {
		return 'Invalid trigger monitor payload.';
	}
	const location = issue.path.length > 0 ? ` (${issue.path.join('.')})` : '';
	return `Invalid trigger monitor payload: ${issue.message}${location}`;
}

async function enforceRequestSizeLimit(request: Request, limitBytes: number) {
	const limitMessage = `Trigger monitor payload exceeds ${limitBytes} bytes.`;

	const contentLength = request.headers.get('content-length');
	if (contentLength) {
		const declared = Number(contentLength);
		if (Number.isFinite(declared) && declared > limitBytes) {
			throw error(413, limitMessage);
		}
	}

	const clone = request.clone();
	const body = clone.body;
	if (!body) {
		return;
	}

	const reader = body.getReader();
	let total = 0;
	try {
		while (true) {
			const { done, value } = await reader.read();
			if (done) {
				break;
			}
			if (value) {
				total += value.byteLength ?? value.length ?? 0;
			}
			if (total > limitBytes) {
				throw error(413, limitMessage);
			}
		}
	} finally {
		reader.releaseLock();
	}
}

export const GET: RequestHandler = async ({ params, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	requireViewer(locals.user);

	try {
		const status = await dispatchTriggerMonitorCommand(id, { action: 'status' });
		return json(status satisfies TriggerMonitorStatus);
	} catch (err) {
		if (err instanceof TriggerMonitorAgentError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to load trigger monitor status');
	}
};

export const POST: RequestHandler = async ({ params, request, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	const user = requireOperator(locals.user);

	await enforceRequestSizeLimit(request, _MAX_TRIGGER_MONITOR_REQUEST_BYTES);

	let payload: unknown;
	try {
		payload = await request.json();
	} catch {
		throw error(400, 'Invalid trigger monitor payload');
	}

	const parsedCommand = triggerMonitorCommandRequestSchema.safeParse(payload);
	if (!parsedCommand.success) {
		throw error(400, formatValidationError(parsedCommand.error));
	}

	const command = parsedCommand.data;
	if (command.action !== 'configure') {
		throw error(405, 'Unsupported trigger monitor operation');
	}

	const normalizedWatchlistResult = triggerMonitorWatchlistInputSchema.safeParse(
		command.config.watchlist
	);
	if (!normalizedWatchlistResult.success) {
		throw error(400, formatValidationError(normalizedWatchlistResult.error));
	}

	const normalized = {
		...command,
		config: {
			...command.config,
			refreshSeconds: Math.max(1, Math.min(command.config.refreshSeconds, 3600)),
			watchlist: normalizedWatchlistResult.data
		}
	} as typeof command;

	try {
		const status = await dispatchTriggerMonitorCommand(id, normalized, {
			operatorId: user.id
		});
		return json(status satisfies TriggerMonitorStatus);
	} catch (err) {
		if (err instanceof TriggerMonitorAgentError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to update trigger monitor configuration');
	}
};
