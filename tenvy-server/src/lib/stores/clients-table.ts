import type { AgentSnapshot } from '../../../../shared/types/agent';
import { agentsStore } from './agents.svelte.js';

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

export class ClientsTableStore {
	searchQuery = $state('');
	statusFilter = $state<StatusFilter>('all');
	tagFilter = $state<TagFilter>('all');
	perPage = $state(10);
	currentPage = $state(1);

	constructor() {
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
		});
	}

	get agents() {
		return agentsStore.agents;
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

	setAgents(nextAgents: AgentSnapshot[]) {
		agentsStore.setAgents(nextAgents);
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
		agentsStore.emitOptimistic(agent);
	}

	isOptimistic(agentId: string) {
		return agentsStore.isOptimistic(agentId);
	}

	clearOptimisticAgent(agentId: string) {
		agentsStore.clearOptimistic(agentId);
	}
}

export function createClientsTableStore(initialAgents: AgentSnapshot[]) {
	const store = new ClientsTableStore();
	if (initialAgents) {
		store.setAgents(initialAgents);
	}
	return store;
}


