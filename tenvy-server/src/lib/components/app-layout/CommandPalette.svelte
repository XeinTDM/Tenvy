<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Kbd from "$lib/components/ui/kbd/index.js";
	import { Search, Monitor, Settings } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { navItems, navSummaries } from '$lib/config/navigation.js';
	import { agentsStore } from '$lib/stores/agents.svelte.js';
	import type { AgentSnapshot } from '../../../../../shared/types/agent.js';
	import type { IconComponent } from '$lib/types/navigation.js';

	type SearchItem = {
		id: string;
		title: string;
		description?: string;
		icon: IconComponent;
		href: string;
		category: 'Navigation' | 'Agents' | 'Settings';
	};

	let { open = $bindable() } = $props<{ open: boolean }>();

	let searchQuery = $state('');

	onMount(() => {
		const handleKeydown = (e: KeyboardEvent) => {
			if (e.key === 'k' && (e.metaKey || e.ctrlKey)) {
				e.preventDefault();
				open = !untrack(() => open);
			}
		};

		window.addEventListener('keydown', handleKeydown);
		return () => {
			window.removeEventListener('keydown', handleKeydown);
		};
	});

	const searchItems = $derived.by(() => {
		const items: SearchItem[] = [];

		navItems.forEach((item) => {
			items.push({
				id: `nav-${item.slug}`,
				title: item.title,
				description: navSummaries[item.slug].description,
				icon: item.icon,
				href: item.href,
				category: 'Navigation'
			});
		});

		agentsStore.agents.forEach((agent: AgentSnapshot) => {
			items.push({
				id: `agent-${agent.id}`,
				title: agent.metadata.hostname || agent.id,
				description: `${agent.metadata.username}@${agent.metadata.ipAddress || 'unknown'} · ${agent.status}`,
				icon: Monitor,
				href: `/clients/${agent.id}`,
				category: 'Agents'
			});
		});

		items.push({
			id: 'settings-general',
			title: 'Console Settings',
			description: 'Preferences and administrative configuration.',
			icon: Settings,
			href: '/settings',
			category: 'Settings'
		});

		return items;
	});

	const filteredSearchItems = $derived.by(() => {
		const query = searchQuery.trim().toLowerCase();
		if (!query) return searchItems;

		return searchItems.filter(
			(item) =>
				item.title.toLowerCase().includes(query) ||
				item.description?.toLowerCase().includes(query) ||
				item.category.toLowerCase().includes(query)
		);
	});

	const searchGroups = $derived.by(() => {
		const groups: Record<string, SearchItem[]> = {};
		filteredSearchItems.forEach((item) => {
			if (!groups[item.category]) groups[item.category] = [];
			groups[item.category].push(item);
		});
		return groups;
	});

	function handleSearchSelect(href: string) {
		open = false;
		searchQuery = '';
		// @ts-ignore - Dynamic navigation
		void goto(resolve(href));
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="overflow-hidden p-0 sm:max-w-2xl">
		<div class="relative flex items-center border-b px-4">
			<Search class="h-4 w-4 shrink-0 text-muted-foreground" />
			<input
				type="text"
				placeholder="Search agents, settings, navigation..."
				class="flex h-12 w-full border-0 bg-transparent px-2 py-3 focus-visible:ring-0 focus:outline-none"
				bind:value={searchQuery}
			/>
			<Kbd.Root class="mr-5">ESC</Kbd.Root>
		</div>
		<div class="max-h-[min(480px,70vh)] overflow-y-auto p-2">
			{#each Object.entries(searchGroups) as [category, items]}
				<div class="px-2 py-2">
					<h3 class="px-2 text-[10px] font-semibold tracking-wider text-muted-foreground uppercase">
						{category}
					</h3>
					<div class="mt-2 space-y-1">
						{#each items as item (item.id)}
							<button
								class="flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm transition-colors hover:bg-muted focus:bg-muted focus:outline-none"
								onclick={() => handleSearchSelect(item.href)}
							>
								<div
									class="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border bg-background"
								>
									<item.icon class="h-4 w-4" />
								</div>
								<div class="flex flex-1 flex-col overflow-hidden">
									<span class="truncate font-medium">{item.title}</span>
									{#if item.description}
										<span class="truncate text-xs text-muted-foreground">{item.description}</span>
									{/if}
								</div>
							</button>
						{/each}
					</div>
				</div>
			{:else}
				<div class="flex flex-col items-center justify-center py-12 text-center">
					<div class="rounded-full bg-muted p-3">
						<Search class="h-6 w-6 text-muted-foreground" />
					</div>
					<p class="mt-4 text-sm text-muted-foreground">
						No results found for "{searchQuery}"
					</p>
				</div>
			{/each}
		</div>
		<div class="flex items-center justify-between border-t bg-muted/50 px-4 py-2 text-[10px]">
			<div class="flex items-center gap-4 text-muted-foreground">
				<span class="flex items-center gap-1">
					<Kbd.Root class="px-1.5 py-0.5">↑↓</Kbd.Root>
					Navigate
				</span>
				<span class="flex items-center gap-1">
					<Kbd.Root class="px-1.5 py-0.5">↵</Kbd.Root>
					Select
				</span>
			</div>
			<div class="text-muted-foreground">
				<span class="font-medium">{filteredSearchItems.length}</span> results
			</div>
		</div>
	</Dialog.Content>
</Dialog.Root>