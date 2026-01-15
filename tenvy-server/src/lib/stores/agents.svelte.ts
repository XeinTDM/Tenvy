import { browser } from '$app/environment';
import type { AgentSnapshot } from '../../../../shared/types/agent.js';
import { registryEventBus, type RegistryEventMessage } from './registry-events.js';

function dedupeAgents(agents: AgentSnapshot[]): AgentSnapshot[] {
	const seen = new Set<string>();
	const result: AgentSnapshot[] = [];
	for (let index = agents.length - 1; index >= 0; index -= 1) {
		const agent = agents[index];
		if (seen.has(agent.id)) {
			continue;
		}
		seen.add(agent.id);
		result.unshift(agent);
	}
	return result;
}

function upsertAgent(list: AgentSnapshot[], next: AgentSnapshot): AgentSnapshot[] {
	const clone = [...list];
	const index = clone.findIndex((agent) => agent.id === next.id);
	if (index === -1) {
		clone.push(next);
	} else {
		clone[index] = next;
	}
	return clone;
}

export function createAgentsStore() {
	let agents = $state<AgentSnapshot[]>([]);
	let isInitialized = $state(false);
	let busUnsubscribe: (() => void) | null = null;
	const optimisticAgents = new Map<string, AgentSnapshot>();

	function applyRegistryEvent(event: RegistryEventMessage) {
		if (!event || typeof event !== 'object') {
			return;
		}

		if (event.type === 'agents') {
			optimisticAgents.clear();
			agents = dedupeAgents(event.agents || []);
			isInitialized = true;
			return;
		}

		if (event.type === 'agent') {
			if (event.optimistic) {
				optimisticAgents.set(event.agent.id, event.agent);
			} else {
				optimisticAgents.delete(event.agent.id);
			}
			agents = dedupeAgents(upsertAgent(agents, event.agent));
		}
	}

	function init(initialAgents?: AgentSnapshot[]) {
		if (!browser) return;
		
		if (initialAgents && !isInitialized) {
			agents = dedupeAgents(initialAgents);
			isInitialized = true;
		}

		if (!busUnsubscribe) {
			busUnsubscribe = registryEventBus.subscribe(applyRegistryEvent);
		}

		return () => {
			if (busUnsubscribe) {
				busUnsubscribe();
				busUnsubscribe = null;
			}
		};
	}

	function emitOptimistic(agent: AgentSnapshot) {
		registryEventBus.emitOptimistic({
			type: 'agent',
			agent
		} as RegistryEventMessage);
	}

	return {
		get agents() { return agents; },
		get isInitialized() { return isInitialized; },
		init,
		setAgents: (next: AgentSnapshot[]) => {
			optimisticAgents.clear();
			agents = dedupeAgents(next);
			isInitialized = true;
		},
		emitOptimistic,
		isOptimistic: (agentId: string) => optimisticAgents.has(agentId),
		clearOptimistic: (agentId: string) => optimisticAgents.delete(agentId)
	};
}

export const agentsStore = createAgentsStore();
