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
		clearStoredPorts,
		formatPortSummary,
		loadStoredPorts,
		persistPortSelection
	} from '$lib/utils/rat-port-preferences.js';
	import {
		Bell,
		Plug,
		Search
	} from '@lucide/svelte';
	import { onMount } from 'svelte';
	import { registryEventBus } from '$lib/stores/registry-events.js';
	import type { AgentSnapshot } from '../../../../shared/types/agent.js';
	import { browser } from '$app/environment';
	import { navSummaries } from '$lib/config/navigation.js';
	import AppSidebar from '$lib/components/app-layout/AppSidebar.svelte';
	import CommandPalette from '$lib/components/app-layout/CommandPalette.svelte';
	import PortConfigDialog from '$lib/components/app-layout/PortConfigDialog.svelte';

	const PORT_SYNC_CHANNEL_NAME = 'tenvy.rat-port-sync';
	const PORT_SYNC_STORAGE_KEY = 'tenvy.rat-port-sync-message';

	type PortSyncPayload =
		| { type: 'state-request'; source: string }
		| { type: 'state-update'; source: string; ports: number[]; remember: boolean }
		| { type: 'state-clear'; source: string };

	type PortSyncMessage =
		| { type: 'state-request' }
		| { type: 'state-update'; ports: number[]; remember: boolean }
		| { type: 'state-clear' };

	let portSyncChannel: BroadcastChannel | null = null;
	let portSyncId: string | null = null;

	function generatePortSyncId(): string {
		if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
			return crypto.randomUUID();
		}

		return Math.random().toString(36).slice(2);
	}

	function postPortSync(message: PortSyncMessage) {
		if (!browser || !portSyncId) {
			return;
		}

		const payload: PortSyncPayload = { ...message, source: portSyncId };

		if (portSyncChannel) {
			portSyncChannel.postMessage(payload);
			return;
		}

		try {
			window.localStorage.setItem(PORT_SYNC_STORAGE_KEY, JSON.stringify(payload));
			window.localStorage.removeItem(PORT_SYNC_STORAGE_KEY);
		} catch {
			// Ignore
		}
	}

	const searchPlaceholders: Partial<Record<NavKey, string>> = {
		clients: 'Search clients, hosts, IPs...'
	};

	const defaultSearchPlaceholder = 'Search clients, plugins, activity...';

	let selectedPorts = $state<number[]>([]);
	let rememberPorts = $state(false);
	let portDialogOpen = $state(false);
	let hasHydrated = $state(false);

	let searchDialogOpen = $state(false);
	let agents = $state<AgentSnapshot[]>([]);

	const portSummary = $derived(() => formatPortSummary(selectedPorts));

	function openPortDialog() {
		portDialogOpen = true;
	}

	function handleSavePorts(ports: number[], remember: boolean) {
		selectedPorts = ports;
		rememberPorts = remember;
		
		persistPortSelection(ports, remember);
		postPortSync({
			type: 'state-update',
			ports: ports,
			remember: remember
		});
	}

	function handleClearPortPreferences() {
		clearStoredPorts();
		selectedPorts = [];
		rememberPorts = false;
		portDialogOpen = true;
		postPortSync({ type: 'state-clear' });
	}

	onMount(() => {
		if (!browser) {
			return;
		}

		const stored = loadStoredPorts();

		if (stored) {
			selectedPorts = stored.ports;
			rememberPorts = stored.remember;
		} else {
			openPortDialog();
		}

		const busUnsubscribe = registryEventBus.subscribe((event) => {
			if (event.type === 'agents') {
				agents = event.agents || [];
			} else if (event.type === 'agent') {
				const idx = agents.findIndex((a) => a.id === event.agent.id);
				if (idx === -1) {
					agents = [...agents, event.agent];
				} else {
					const next = [...agents];
					next[idx] = event.agent;
					agents = next;
				}
			}
		});

		hasHydrated = true;

		portSyncId = generatePortSyncId();

		const handleIncoming = (payload: PortSyncPayload | null | undefined) => {
			if (!payload || payload.source === portSyncId) {
				return;
			}

			if (payload.type === 'state-request') {
				if (selectedPorts.length > 0) {
					postPortSync({
						type: 'state-update',
						ports: selectedPorts,
						remember: rememberPorts
					});
				} else {
					postPortSync({ type: 'state-clear' });
				}
				return;
			}

			if (payload.type === 'state-update') {
				selectedPorts = payload.ports;
				rememberPorts = payload.remember;

				if (payload.ports.length > 0) {
					portDialogOpen = false;
				}

				persistPortSelection(payload.ports, payload.remember);
				return;
			}

			if (payload.type === 'state-clear') {
				selectedPorts = [];
				rememberPorts = false;

				if (!portDialogOpen) {
					portDialogOpen = true;
				}

				persistPortSelection([], false);
			}
		};

		const storageListener = (event: StorageEvent) => {
			if (event.key !== PORT_SYNC_STORAGE_KEY || !event.newValue) {
				return;
			}

			try {
				const payload = JSON.parse(event.newValue) as PortSyncPayload;
				handleIncoming(payload);
			} catch {
				// Ignore malformed sync messages.
			}
		};

		window.addEventListener('storage', storageListener);

		let channel: BroadcastChannel | null = null;
		let channelListener: ((event: MessageEvent<PortSyncPayload>) => void) | null = null;

		if ('BroadcastChannel' in window) {
			channel = new BroadcastChannel(PORT_SYNC_CHANNEL_NAME);
			channelListener = (event) => handleIncoming(event.data);
			channel.addEventListener('message', channelListener);
			portSyncChannel = channel;
		} else {
			portSyncChannel = null;
		}

		queueMicrotask(() => {
			postPortSync({ type: 'state-request' });
		});

		return () => {
			window.removeEventListener('storage', storageListener);
			busUnsubscribe();

			if (channel && channelListener) {
				channel.removeEventListener('message', channelListener);
				channel.close();
			}

			if (portSyncChannel === channel) {
				portSyncChannel = null;
			}

			portSyncId = null;
		};
	});

	$effect(() => {
		if (hasHydrated && !portDialogOpen && selectedPorts.length === 0) {
			portDialogOpen = true;
		}
	});

	type NavigationBadgeMap = Partial<Record<NavKey, string>>;

	type LayoutData = {
		activeNav: NavKey;
		user: AuthenticatedUser;
		navBadges?: NavigationBadgeMap;
	};

	let { children, data: layoutData } = $props<{ data: LayoutData }>();

	const activeSummary = $derived(() => {
		const { activeNav } = layoutData as LayoutData;
		return navSummaries[activeNav];
	});

	const globalSearchPlaceholder = $derived(() => {
		const { activeNav } = layoutData as LayoutData;
		return searchPlaceholders[activeNav] ?? defaultSearchPlaceholder;
	});

	const navBadgeMap = $derived(
		() => (layoutData as LayoutData)?.navBadges ?? ({} as NavigationBadgeMap)
	);
</script>

<SidebarProvider>
	<AppSidebar 
		activeNav={layoutData.activeNav} 
		user={layoutData.user} 
		navBadges={navBadgeMap()} 
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
							<span class="truncate">{globalSearchPlaceholder()}</span>
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
						selectedPorts.length === 0 &&
							'border-dashed border-destructive/60 text-destructive hover:text-destructive'
					)}
					title={selectedPorts.length > 0
						? `RAT listening ports: ${portSummary()}`
						: 'Select RAT listening ports'}
					onclick={() => openPortDialog()}
				>
					<Plug class="h-4 w-4 shrink-0" />
					{#if selectedPorts.length > 0}
						<span class="text-xs tracking-wide text-muted-foreground uppercase">Ports</span>
						<span class="truncate text-sm font-medium">{portSummary()}</span>
						{#if rememberPorts}
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
						selectedPorts.length === 0 &&
							'border-dashed border-destructive/60 text-destructive hover:text-destructive'
					)}
					title={selectedPorts.length > 0
						? `Listening ports: ${portSummary()}`
						: 'Select listening ports'}
					onclick={() => openPortDialog()}
				>
					<Plug class="h-4 w-4" />
					<span class="sr-only">
						{selectedPorts.length > 0
							? `Update listening ports (${portSummary()}${rememberPorts ? ', remembered preference' : ''})`
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
				{#key (layoutData as LayoutData).activeNav}
					{@const summary = activeSummary()}
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
		bind:open={portDialogOpen} 
		{selectedPorts} 
		{rememberPorts}
		onSave={handleSavePorts}
		onClear={handleClearPortPreferences}
	/>

	<CommandPalette 
		bind:open={searchDialogOpen} 
		{agents} 
	/>
</SidebarProvider>
<Toaster position="bottom-right" />
