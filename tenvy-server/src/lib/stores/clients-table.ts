import type { AgentSnapshot } from '../../../../shared/types/agent';
import { registryEventBus, type RegistryEventMessage } from './registry-events';

export type StatusFilter = 'all' | AgentSnapshot['status'];
export type TagFilter = 'all' | string;

export type PageRange = { start: number; end: number };

function sanitizeQuery(query: string): string {
	return query.trim().toLowerCase();
}

function matchesStatus(agent: AgentSnapshot, filter: StatusFilter): boolean {
	return filter === 'all' || agent.status === filter;
}

function matchesTag(agent: AgentSnapshot, filter: TagFilter): boolean {
	if (filter === 'all') {
		return true;
	}

	return agent.metadata.tags?.some((tag) => tag.toLowerCase() === filter.toLowerCase()) ?? false;
}

function matchesQuery(agent: AgentSnapshot, query: string): boolean {
	if (!query) {
		return true;
	}

	const haystack = [
		agent.id,
		agent.metadata.hostname,
		agent.metadata.username,
		agent.metadata.os,
		agent.metadata.ipAddress,
		agent.metadata.publicIpAddress,
		...(agent.metadata.tags ?? [])
	]
		.filter(Boolean)
		.map((value) => value!.toString().toLowerCase());

	return haystack.some((value) => value.includes(query));
}

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

export class ClientsTableStore {
	agents = $state<AgentSnapshot[]>([]);
	searchQuery = $state('');
	statusFilter = $state<StatusFilter>('all');
	tagFilter = $state<TagFilter>('all');
	perPage = $state(10);
	currentPage = $state(1);

	private busUnsubscribe: (() => void) | null = null;
	private optimisticAgents = new Map<string, AgentSnapshot>();

	constructor(initialAgents: AgentSnapshot[]) {
		this.agents = dedupeAgents(initialAgents ?? []);
		
		$effect.root(() => {
			$effect(() => {
				// Reset to page 1 when filters change
				// eslint-disable-next-line @typescript-eslint/no-unused-expressions
				this.searchQuery;
				// eslint-disable-next-line @typescript-eslint/no-unused-expressions
				this.statusFilter;
				// eslint-disable-next-line @typescript-eslint/no-unused-expressions
				this.tagFilter;
				// eslint-disable-next-line @typescript-eslint/no-unused-expressions
				this.perPage;
				
				this.currentPage = 1;
			});

			this.startBus();
			return () => this.stopBus();
		});
	}

	availableTags = $derived.by(() => {
		const tags = new Set<string>();
		for (const agent of this.agents) {
			for (const tag of agent.metadata.tags ?? []) {
				tags.add(tag);
			}
		}
		return Array.from(tags).sort((a, b) => a.localeCompare(b));
	});

	filteredAgents = $derived.by(() => {
		const normalizedQuery = sanitizeQuery(this.searchQuery);
		return this.agents.filter(
			(agent) =>
				matchesStatus(agent, this.statusFilter) &&
				matchesTag(agent, this.tagFilter) &&
				matchesQuery(agent, normalizedQuery)
		);
	});

	totalPages = $derived(
		this.filteredAgents.length === 0
			? 1
			: Math.max(1, Math.ceil(this.filteredAgents.length / Math.max(1, this.perPage)))
	);

	paginatedAgents = $derived.by(() => {
		const safePerPage = Math.max(1, this.perPage);
		const safePage = Math.min(Math.max(this.currentPage, 1), this.totalPages);
		const startIndex = (safePage - 1) * safePerPage;
		return this.filteredAgents.slice(startIndex, startIndex + safePerPage);
	});

	pageRange = $derived.by(() => {
		if (this.filteredAgents.length === 0 || this.paginatedAgents.length === 0) {
			return { start: 0, end: 0 };
		}
		const startIndex = (Math.min(Math.max(this.currentPage, 1), this.totalPages) - 1) * Math.max(1, this.perPage);
		const start = startIndex + 1;
		const end = Math.min(startIndex + this.paginatedAgents.length, this.filteredAgents.length);
		return { start, end };
	});

	paginationItems = $derived.by(() => {
		const total = this.totalPages;
		const current = Math.min(Math.max(this.currentPage, 1), total);
		const siblingCount = 1;

		if (total <= 1) {
			return [1];
		}

		const start = Math.max(2, current - siblingCount);
		const end = Math.min(total - 1, current + siblingCount);

		const items: (number | 'ellipsis')[] = [1];

		if (start > 2) {
			items.push('ellipsis');
		}

		for (let page = start; page <= end; page += 1) {
			items.push(page);
		}

		if (end < total - 1) {
			items.push('ellipsis');
		}

		items.push(total);

		return items;
	});

	private applyRegistryEvent = (event: RegistryEventMessage) => {
		if (!event || typeof event !== 'object') {
			return;
		}

		if (event.type === 'agents') {
			this.optimisticAgents.clear();
			this.agents = dedupeAgents(event.agents ?? []);
			return;
		}

		if (event.type === 'agent') {
			if (event.optimistic) {
				this.optimisticAgents.set(event.agent.id, event.agent);
			} else {
				this.optimisticAgents.delete(event.agent.id);
			}
			this.agents = dedupeAgents(upsertAgent(this.agents, event.agent));
		}
	};

	private startBus() {
		if (this.busUnsubscribe) return;
		this.busUnsubscribe = registryEventBus.subscribe(this.applyRegistryEvent);
	}

	private stopBus() {
		if (this.busUnsubscribe) {
			this.busUnsubscribe();
			this.busUnsubscribe = null;
		}
		this.optimisticAgents.clear();
	}

	setAgents(nextAgents: AgentSnapshot[]) {
		this.optimisticAgents.clear();
		this.agents = dedupeAgents(nextAgents ?? []);
	}

	setSearchQuery(value: string) {
		this.searchQuery = value;
	}

	setStatusFilter(value: StatusFilter) {
		this.statusFilter = value;
	}

	setTagFilter(value: TagFilter) {
		this.tagFilter = value;
	}

	setPerPage(value: number) {
		this.perPage = Math.max(1, value);
	}

	goToPage(page: number) {
		this.currentPage = Math.min(Math.max(1, Math.trunc(page)), this.totalPages);
	}

	nextPage() {
		this.currentPage = Math.min(this.totalPages, this.currentPage + 1);
	}

	previousPage() {
		this.currentPage = Math.max(1, this.currentPage - 1);
	}

	optimisticUpdateAgent(agent: AgentSnapshot) {
		registryEventBus.emitOptimistic({
			type: 'agent',
			agent
		} as RegistryEventMessage);
	}

	isOptimistic(agentId: string) {
		return this.optimisticAgents.has(agentId);
	}

	clearOptimisticAgent(agentId: string) {
		this.optimisticAgents.delete(agentId);
	}
}

export function createClientsTableStore(initialAgents: AgentSnapshot[]) {
	return new ClientsTableStore(initialAgents);
}

