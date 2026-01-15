<script lang="ts">
	import { cn } from '$lib/utils.js';
	import {
		SidebarInset,
		SidebarProvider,
		SidebarTrigger
	} from '$lib/components/ui/sidebar/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import {
		Breadcrumb,
		BreadcrumbItem,
		BreadcrumbLink,
		BreadcrumbList,
		BreadcrumbPage,
		BreadcrumbSeparator
	} from '$lib/components/ui/breadcrumb/index.js';
	import { Toaster } from '$lib/components/ui/sonner/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { Separator } from '$lib/components/ui/separator/index.js';
	import * as Kbd from "$lib/components/ui/kbd/index.js";
	import type { NavKey } from '$lib/types/navigation.js';
	import type { AuthenticatedUser } from '$lib/server/auth';
	import {
		Bell,
		Plug,
		Search
	} from '@lucide/svelte';
	import { onMount } from 'svelte';
	import { agentsStore } from '$lib/stores/agents.svelte.js';
	import type { AgentSnapshot } from '../../../../shared/types/agent.js';
	import { browser } from '$app/environment';
	import { navSummaries } from '$lib/config/navigation.js';
	import AppSidebar from '$lib/components/app-layout/AppSidebar.svelte';
	import CommandPalette from '$lib/components/app-layout/CommandPalette.svelte';
	import PortConfigDialog from '$lib/components/app-layout/PortConfigDialog.svelte';
	import { createPortSyncStore } from '$lib/stores/rat-port-sync.svelte.js';

	const searchPlaceholders: Partial<Record<NavKey, string>> = {
		clients: 'Search clients, hosts, IPs...'
	};

	const defaultSearchPlaceholder = 'Search clients, plugins, activity...';

	const portStore = createPortSyncStore();
	let searchDialogOpen = $state(false);

	onMount(() => {
		if (!browser) return;

		const cleanupPortStore = portStore.init();
		const cleanupAgentsStore = agentsStore.init();

		return () => {
			cleanupPortStore?.();
			cleanupAgentsStore?.();
		};
	});

	$effect(() => {
		if (portStore.hasHydrated && !portStore.portDialogOpen && portStore.selectedPorts.length === 0) {
			portStore.portDialogOpen = true;
		}
	});

	type NavigationBadgeMap = Partial<Record<NavKey, string>>;

	type LayoutData = {
		activeNav: NavKey;
		user: AuthenticatedUser;
		navBadges?: NavigationBadgeMap;
		agents?: AgentSnapshot[];
	};

	let { children, data: layoutData } = $props<{ data: LayoutData }>();

	$effect(() => {
		if (layoutData.agents) {
			agentsStore.setAgents(layoutData.agents);
		}
	});

	const activeSummary = $derived(navSummaries[layoutData.activeNav as NavKey]);

	const globalSearchPlaceholder = $derived(
		searchPlaceholders[layoutData.activeNav as NavKey] ?? defaultSearchPlaceholder
	);

	const navBadgeMap = $derived(layoutData?.navBadges ?? ({} as NavigationBadgeMap));
</script>

<SidebarProvider>
	<AppSidebar 
		activeNav={layoutData.activeNav} 
		user={layoutData.user} 
		navBadges={navBadgeMap} 
	/>
	<SidebarInset>
		<header class="flex h-16 shrink-0 items-center gap-3 border-b">
			<SidebarTrigger class="md:hidden" />
			<Separator orientation="vertical" class="h-6" />
			<div class="flex flex-1 items-center gap-3">
				<div class="relative w-full max-w-md">
					<Button
						variant="outline"
						class="w-full justify-start gap-2 px-3 font-normal text-muted-foreground hover:bg-muted/50"
						onclick={() => (searchDialogOpen = true)}
					>
						<Search class="h-4 w-4 shrink-0" />
						<span class="inline-flex flex-1 items-center justify-between">
							<span class="truncate">{globalSearchPlaceholder}</span>
							<Kbd.Group class="">
								<Kbd.Root>⌘</Kbd.Root>
								<Kbd.Root>K</Kbd.Root>
							</Kbd.Group>
						</span>
					</Button>
				</div>
				<Button
					type="button"
					variant="outline"
					class={cn(
						'hidden max-w-xs items-center gap-2 truncate whitespace-nowrap sm:inline-flex',
						portStore.selectedPorts.length === 0 &&
							'border-dashed border-destructive/60 text-destructive hover:text-destructive'
					)}
					title={portStore.selectedPorts.length > 0
						? `RAT listening ports: ${portStore.portSummary}`
						: 'Select RAT listening ports'}
					onclick={() => portStore.portDialogOpen = true}
				>
					<Plug class="h-4 w-4 shrink-0" />
					{#if portStore.selectedPorts.length > 0}
						<span class="text-xs tracking-wide text-muted-foreground uppercase">Ports</span>
						<span class="truncate text-sm font-medium">{portStore.portSummary}</span>
						{#if portStore.rememberPorts}
							<Badge
								variant="outline"
								class="hidden text-[10px] tracking-wide text-muted-foreground uppercase xl:inline-flex"
							>
								Remembered
							</Badge>
						{/if}
					{:else}
						<span class="text-sm font-medium">Select RAT ports</span>
					{/if}
				</Button>
				<Button
					type="button"
					variant="outline"
					size="icon"
					class={cn(
						'sm:hidden',
						portStore.selectedPorts.length === 0 &&
							'border-dashed border-destructive/60 text-destructive hover:text-destructive'
					)}
					title={portStore.selectedPorts.length > 0
						? `Listening ports: ${portStore.portSummary}`
						: 'Select listening ports'}
					onclick={() => portStore.portDialogOpen = true}
				>
					<Plug class="h-4 w-4" />
					<span class="sr-only">
						{portStore.selectedPorts.length > 0
							? `Update listening ports (${portStore.portSummary}${portStore.rememberPorts ? ', remembered preference' : ''})`
							: 'Configure listening ports'}
					</span>
				</Button>
				<Button variant="ghost">
					<Bell class="h-4 w-4" />
					<span class="sr-only">Notifications</span>
				</Button>
			</div>
		</header>
		<div class="flex flex-1 flex-col overflow-hidden">
			<div class="flex flex-1 flex-col gap-8 overflow-hidden p-6">
				{#key layoutData.activeNav}
					{@const summary = activeSummary}
					<section class="flex flex-wrap items-center justify-between gap-4">
						<Breadcrumb>
							<BreadcrumbList>
								<BreadcrumbItem>
									<BreadcrumbLink href="/dashboard">Console</BreadcrumbLink>
								</BreadcrumbItem>
								<BreadcrumbSeparator />
								<BreadcrumbItem>
									<Tooltip.Provider>
										<Tooltip.Root>
											<Tooltip.Trigger class="cursor-help">
												<BreadcrumbPage>
													{summary.title}
												</BreadcrumbPage>
											</Tooltip.Trigger>
											<Tooltip.Content side="right" align="center">
												<p>{summary.description}</p>
											</Tooltip.Content>
										</Tooltip.Root>
									</Tooltip.Provider>
								</BreadcrumbItem>
							</BreadcrumbList>
						</Breadcrumb>
					</section>
					<div class="flex min-h-0 flex-1 flex-col gap-8">
						{@render children?.()}
					</div>
				{/key}
			</div>
		</div>
	</SidebarInset>
	
	<PortConfigDialog 
		bind:open={portStore.portDialogOpen} 
		selectedPorts={portStore.selectedPorts} 
		rememberPorts={portStore.rememberPorts}
		onSave={portStore.savePorts}
		onClear={portStore.clearPorts}
	/>

	<CommandPalette 
		bind:open={searchDialogOpen} 
	/>
</SidebarProvider>
<Toaster position="bottom-right" />