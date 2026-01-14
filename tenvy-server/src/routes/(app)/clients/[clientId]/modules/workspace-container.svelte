<script lang="ts">
	import { SvelteMap } from 'svelte/reactivity';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button/index.js';
	import {
		Card,
		CardContent,
		CardDescription,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { Separator } from '$lib/components/ui/separator/index.js';
	import { cn } from '$lib/utils.js';
	import type { Client } from '$lib/data/clients';
	import {
		buildClientToolUrl,
		type ClientToolDefinition,
		type ClientToolId
	} from '$lib/data/client-tools';
	import ClientToolWorkspace from '$lib/components/workspace/client-tool-workspace.svelte';
	import { isWorkspaceTool } from '$lib/data/client-tool-workspaces';
	import type { AgentSnapshot } from '../../../../../../../shared/types/agent';
	import { ArrowLeft, X } from '@lucide/svelte';
	import type { Snippet } from 'svelte';

	export interface $$Slots {
		empty?: () => Snippet;
	}

	let {
		client,
		agent = null,
		tools,
		activeTool = null,
		segments = [],
		empty
	} = $props<
		{
			client: Client;
			agent?: AgentSnapshot | null;
			tools: ClientToolDefinition[];
			activeTool?: ClientToolDefinition | null;
			segments?: string[];
		},
		Record<string, never>,
		$$Slots
	>();

	const categoryLabels: Record<string, string> = {
		overview: 'Overview',
		control: 'Control',
		management: 'Management',
		operations: 'Operations',
		misc: 'Miscellaneous',
		'system-controls': 'System Controls',
		power: 'Power'
	};

	type Group = { key: string; label: string; items: ClientToolDefinition[] };

	const groupedTools = $derived(() => {
		const order: Group[] = [];
		const index = new SvelteMap<string, Group>();

		for (const tool of tools) {
			const key = tool.segments[0] ?? 'misc';
			let group = index.get(key);
			if (!group) {
				group = {
					key,
					label:
						categoryLabels[key] ??
						key.replace(/-/g, ' ').replace(/\b\w/g, (char: string) => char.toUpperCase()),
					items: []
				} satisfies Group;
				index.set(key, group);
				order.push(group);
			}
			group.items.push(tool);
		}

		return order.map((group) => ({
			...group,
			items: group.items.slice()
		}));
	}) as unknown as Group[];

	const activeToolId = $derived(() => activeTool?.id ?? null) as unknown as string | null;

	function toWorkspaceUrl(tool: ClientToolDefinition) {
		return buildClientToolUrl(client.id, tool);
	}

	function closeWorkspace() {
		goto(resolve(`/clients/${client.id}/modules` as any));
	}

	function returnToClients() {
		goto(resolve('/clients' as any));
	}

	let workspaceLog = $state<WorkspaceLogEntry[]>([]);
	const logStatusMeta: Record<WorkspaceLogEntry['status'], string> = {
		draft: 'bg-muted text-muted-foreground border-transparent',
		queued: 'bg-amber-500/10 text-amber-600 border-amber-500/30',
		pending: 'bg-sky-500/10 text-sky-600 border-sky-500/30',
		'in-progress': 'bg-primary/10 text-primary border-primary/30 animate-pulse',
		complete: 'bg-emerald-500/10 text-emerald-600 border-emerald-500/30',
		failed: 'bg-rose-500/10 text-rose-600 border-rose-500/30'
	};

	function handleLogChange(next: WorkspaceLogEntry[]) {
		workspaceLog = next;
	}
</script>

<section class="space-y-6">
	<div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
		<div>
			<h1 class="text-2xl font-semibold tracking-tight">{client.codename}</h1>
			<p class="text-sm text-muted-foreground">
				Manage {client.codename}&rsquo;s capabilities without leaving the client workspace.
			</p>
		</div>
		<div class="flex flex-wrap items-center gap-2">
			<Button variant="outline" onclick={returnToClients} class="gap-2">
				<ArrowLeft class="h-4 w-4" />
				<span>Client overview</span>
			</Button>
			{#if activeTool}
				<Button variant="secondary" onclick={closeWorkspace} class="gap-2">
					<X class="h-4 w-4" />
					<span>Close workspace</span>
				</Button>
			{/if}
		</div>
	</div>

	<div class="grid gap-6 lg:grid-cols-[260px_minmax(0,1fr)]">
		<aside class="flex flex-col gap-6">
			<div class="space-y-6 rounded-lg border border-border/60 bg-background/40 p-4">
				{#each groupedTools as group, index (group.key)}
					<div class="space-y-2">
						<p class="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
							{group.label}
						</p>
						<div class="flex flex-col gap-1">
							{#each group.items as item (item.id)}
								{@const isActive = activeToolId === item.id}
								<a
									class={cn(
										'flex items-center justify-between rounded-md border border-transparent px-3 py-2 text-sm transition hover:border-primary/40 hover:bg-primary/5',
										isActive
											? 'border-primary/60 bg-primary/10 text-primary'
											: 'text-muted-foreground'
									)}
									href={resolve(toWorkspaceUrl(item) as any)}
								>
									<span class="truncate">{item.title}</span>
									{#if isWorkspaceTool(item.id as ClientToolId)}
										<span
											class={cn(
												'text-[0.65rem] font-medium tracking-wide uppercase',
												isActive ? 'text-primary' : 'text-muted-foreground/70'
											)}
										>
											Workspace
										</span>
									{/if}
								</a>
							{/each}
						</div>
					</div>
					{#if index < groupedTools.length - 1}
						<Separator />
					{/if}
				{/each}
			</div>

			{#if workspaceLog.length > 0}
				<div class="flex flex-1 flex-col gap-3 rounded-lg border border-border/60 bg-muted/20 p-4">
					<div class="flex items-center justify-between">
						<p class="text-[10px] font-semibold tracking-widest text-muted-foreground uppercase">
							Activity log
						</p>
						<Badge variant="outline" class="h-5 px-1.5 font-mono text-[9px]">
							{workspaceLog.length}
						</Badge>
					</div>
					<div class="space-y-3">
						{#each workspaceLog as entry (entry.id)}
							<div class="space-y-1">
								<div class="flex items-center justify-between gap-2">
									<span class="truncate text-[11px] font-medium text-foreground">
										{entry.action}
									</span>
									<Badge
										variant="outline"
										class={cn(
											'h-4 border px-1 text-[8px] font-semibold tracking-tight uppercase',
											logStatusMeta[entry.status]
										)}
									>
										{entry.status}
									</Badge>
								</div>
								{#if entry.detail}
									<p class="line-clamp-2 text-[10px] leading-tight text-muted-foreground">
										{entry.detail}
									</p>
								{/if}
							</div>
						{/each}
					</div>
				</div>
			{/if}
		</aside>

		<div class="space-y-4">
			{#if activeTool}
				<Card class="border-border/60 bg-background/60 shadow-sm">
					<CardHeader class="space-y-1">
						<div class="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
							<div class="space-y-1">
								<CardTitle>{activeTool.title}</CardTitle>
								{#if segments.length > 0}
									<CardDescription>{segments.join(' / ')}</CardDescription>
								{/if}
							</div>
							<div class="flex items-center gap-2">
								<Button variant="outline" size="sm" onclick={closeWorkspace} class="gap-2">
									<X class="h-4 w-4" />
									<span>Close</span>
								</Button>
							</div>
						</div>
					</CardHeader>
					<CardContent class="space-y-4">
						{#key `${client.id}-${activeTool.id}`}
							<ClientToolWorkspace
								{client}
								{agent}
								tool={activeTool}
								onLogChange={handleLogChange}
							/>
						{/key}
					</CardContent>
				</Card>
			{:else if empty}
				{@render empty!()}
			{:else}
				<Card class="border-dashed">
					<CardHeader>
						<CardTitle>Select a module</CardTitle>
						<CardDescription>
							Choose a capability to launch its dedicated workspace for {client.codename}.
						</CardDescription>
					</CardHeader>
					<CardContent class="space-y-3 text-sm text-muted-foreground">
						<p>Workspaces preserve each tool&rsquo;s state while you evaluate remote workflows.</p>
						<p>
							Use the navigation panel to switch between modules or close the workspace when
							you&rsquo;re done.
						</p>
					</CardContent>
				</Card>
			{/if}
		</div>
	</div>
</section>
