import {
	environmentSnapshotSchema,
	environmentMutationResultSchema,
	environmentCommandRequestSchema,
	type EnvironmentVariableScope
} from '$lib/types/environment';

interface FetchEnvironmentOptions {
	signal?: AbortSignal;
}

interface SetEnvironmentInput {
	key: string;
	value: string;
	scope?: EnvironmentVariableScope;
	restartProcesses?: boolean;
	signal?: AbortSignal;
}

interface RemoveEnvironmentInput {
	key: string;
	scope?: EnvironmentVariableScope;
	signal?: AbortSignal;
}

async function parseError(response: Response) {
	let message = response.statusText || 'Request failed';
	try {
		const payload = (await response.json()) as { message?: string; error?: string };
		message = payload?.message || payload?.error || message;
	} catch {
		// ignore JSON parse errors
	}
	return new Error(message);
}

export async function fetchEnvironmentSnapshot(
	agentId: string,
	options: FetchEnvironmentOptions = {}
) {
	const response = await fetch(`/api/agents/${agentId}/misc/environment-variables`, {
		signal: options.signal
	});
	if (!response.ok) {
		throw await parseError(response);
	}
	const data = await response.json();
	return environmentSnapshotSchema.parse(data);
}

export async function setEnvironmentVariable(agentId: string, input: SetEnvironmentInput) {
	const body = environmentCommandRequestSchema.parse({
		action: 'set',
		key: input.key,
		value: input.value,
		scope: input.scope ?? 'user',
		restartProcesses: input.restartProcesses ?? false,
		timestamp: new Date().toISOString()
	});

	const response = await fetch(`/api/agents/${agentId}/misc/environment-variables`, {
		method: 'POST',
		signal: input.signal,
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body)
	});

	if (!response.ok) {
		throw await parseError(response);
	}

	const data = await response.json();
	return environmentMutationResultSchema.parse(data);
}

export async function removeEnvironmentVariable(agentId: string, input: RemoveEnvironmentInput) {
	const body = environmentCommandRequestSchema.parse({
		action: 'remove',
		key: input.key,
		scope: input.scope ?? 'user',
		timestamp: new Date().toISOString()
	});

	const response = await fetch(`/api/agents/${agentId}/misc/environment-variables`, {
		method: 'POST',
		signal: input.signal,
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body)
	});

	if (!response.ok) {
		throw await parseError(response);
	}

	const data = await response.json();
	return environmentMutationResultSchema.parse(data);
}
