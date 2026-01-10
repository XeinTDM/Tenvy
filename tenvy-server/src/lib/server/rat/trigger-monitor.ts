import type {
	TriggerMonitorCommandRequest,
	TriggerMonitorCommandResponse,
	TriggerMonitorStatus
} from '$lib/types/trigger-monitor';
import {
	triggerMonitorCommandRequestSchema,
	triggerMonitorCommandResponseSchema
} from '$lib/types/trigger-monitor';
import { registry, RegistryError } from './store';

const DEFAULT_TIMEOUT_MS = 8_000;
const CONFIGURE_TIMEOUT_MS = 20_000;
const MAX_TIMEOUT_MS = 60_000;
const POLL_INTERVAL_MS = 200;

export class TriggerMonitorAgentError extends Error {
	status: number;
	code?: string;

	constructor(message: string, status = 500, options: { code?: string } = {}) {
		super(message);
		this.name = 'TriggerMonitorAgentError';
		this.status = status;
		this.code = options.code;
	}
}

interface DispatchOptions {
	operatorId?: string;
	timeoutMs?: number;
}

type TriggerMonitorRequestResult = TriggerMonitorStatus;

type CommandResultSnapshot = {
	commandId: string;
	success: boolean;
	output?: string;
	error?: string;
	completedAt: string;
};

function normalizeTimeout(action: TriggerMonitorCommandRequest['action'], requested?: number) {
	const baseline = action === 'status' ? DEFAULT_TIMEOUT_MS : CONFIGURE_TIMEOUT_MS;
	if (typeof requested !== 'number' || Number.isNaN(requested) || requested <= 0) {
		return baseline;
	}
	const clamped = Math.min(Math.max(Math.floor(requested), 1_000), MAX_TIMEOUT_MS);
	return Math.max(clamped, baseline);
}

async function waitForCommandResult(agentId: string, commandId: string, timeoutMs: number) {
	const start = Date.now();
	let delay = POLL_INTERVAL_MS;

	while (Date.now() - start <= timeoutMs) {
		let snapshot: ReturnType<typeof registry.getAgent>;
		try {
			snapshot = registry.getAgent(agentId);
		} catch (err) {
			if (err instanceof RegistryError) {
				throw new TriggerMonitorAgentError(err.message, err.status);
			}
			throw err;
		}

		const match = snapshot.recentResults.find((entry) => entry.commandId === commandId);
		if (match) {
			return match;
		}

		const elapsed = Date.now() - start;
		const remaining = timeoutMs - elapsed;
		if (remaining <= 0) {
			break;
		}

		await new Promise((resolve) => setTimeout(resolve, Math.min(delay, remaining)));
		delay = Math.min(delay * 2, 1_000);
	}

	throw new TriggerMonitorAgentError('Timed out waiting for trigger monitor response', 504);
}

function extractResult<T extends TriggerMonitorCommandRequest>(
	request: T,
	decoded: TriggerMonitorCommandResponse
): TriggerMonitorRequestResult {
	if (decoded.action !== request.action) {
		throw new TriggerMonitorAgentError('Agent returned mismatched trigger monitor action', 502);
	}

	if (decoded.status === 'error') {
		throw new TriggerMonitorAgentError(
			decoded.error || 'Agent reported trigger monitor failure',
			502,
			{ code: decoded.code }
		);
	}

	if (decoded.status !== 'ok') {
		throw new TriggerMonitorAgentError('Agent returned unknown trigger monitor response', 502);
	}

	if (!('result' in decoded)) {
		throw new TriggerMonitorAgentError('Agent response missing trigger monitor payload', 502);
	}

	return decoded.result;
}

export async function dispatchTriggerMonitorCommand<T extends TriggerMonitorCommandRequest>(
	agentId: string,
	request: T,
	options: DispatchOptions = {}
): Promise<TriggerMonitorRequestResult> {
	const payload = triggerMonitorCommandRequestSchema.parse(request);

	let queuedCommandId: string;
	try {
		const queued = registry.queueCommand(
			agentId,
			{ name: 'trigger-monitor', payload },
			{ operatorId: options.operatorId }
		);
		queuedCommandId = queued.command.id;
	} catch (err) {
		if (err instanceof RegistryError) {
			throw new TriggerMonitorAgentError(err.message, err.status);
		}
		throw new TriggerMonitorAgentError('Failed to queue trigger monitor command', 500);
	}

	const timeout = normalizeTimeout(request.action, options.timeoutMs);
	let result: CommandResultSnapshot;
	try {
		result = await waitForCommandResult(agentId, queuedCommandId, timeout);
	} catch (err) {
		if (err instanceof RegistryError) {
			throw new TriggerMonitorAgentError(err.message, err.status);
		}
		throw err;
	}

	if (!result.success) {
		throw new TriggerMonitorAgentError(
			result.error || 'Agent failed to execute trigger monitor command',
			502
		);
	}

	if (!result.output) {
		throw new TriggerMonitorAgentError('Agent response missing trigger monitor payload', 502);
	}

	let decoded: TriggerMonitorCommandResponse;
	try {
		const parsed = JSON.parse(result.output) as unknown;
		decoded = triggerMonitorCommandResponseSchema.parse(parsed);
	} catch (err) {
		throw new TriggerMonitorAgentError(
			`Trigger monitor response payload malformed: ${(err as Error).message || 'invalid JSON'}`,
			502
		);
	}

	return extractResult(request, decoded);
}
