<script lang="ts">
	import { cn } from '$lib/utils.js';
	import {
		Sidebar,
		SidebarContent,
		SidebarFooter,
		SidebarHeader,
		SidebarMenu,
		SidebarMenuBadge,
		SidebarMenuButton,
		SidebarMenuItem,
		SidebarRail
	} from '$lib/components/ui/sidebar/index.js';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Separator } from '$lib/components/ui/separator/index.js';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover/index.js';
	import { Settings, User, LogOut, Sun, Moon, ChevronDown } from '@lucide/svelte';
	import { toggleMode } from 'mode-watcher';
	import { resolve } from '$app/paths';
	import { navItems } from '$lib/config/navigation.js';
	import type { NavKey } from '$lib/types/navigation.js';
	import type { ComponentProps } from 'svelte';
	import type { AuthenticatedUser } from '$lib/server/auth';
	import { goto } from '$app/navigation';

	type SidebarMenuButtonChildContext = Parameters<
		NonNullable<ComponentProps<typeof SidebarMenuButton>['child']>
	>[0];

	let { activeNav, user, navBadges = {} } = $props<{
		activeNav: NavKey;
		user: AuthenticatedUser;
		navBadges?: Partial<Record<NavKey, string>>;
	}>();

	let isUserMenuOpen = $state(false);

	function formatIdentifier(value: string) {
		const cleaned = value.replace(/[^a-z0-9]/gi, '');
		const slice = cleaned.slice(0, 2);
		return slice ? slice.toUpperCase() : 'OP';
	}

	const operatorInitials = $derived(() => formatIdentifier(user?.id ?? ''));

	const operatorLabel = $derived(() => {
		const id = user?.id ?? '';
		return id ? `XeinTDM ${id.slice(0, 6).toUpperCase()}` : 'XeinTDM';
	});

	const voucherDescriptor = $derived(() => {
		if (!user?.voucherId) {
			return 'Unavailable';
		}
		const truncated =
			user.voucherId.length > 10 ? `${user.voucherId.slice(0, 10)}…` : user.voucherId;
		return `${truncated} · ${user.voucherActive ? 'Voucher active' : 'Voucher inactive'}`;
	});

	const voucherStatusBadgeVariant = $derived(() => {
		const active = user?.voucherActive;
		if (active === false) {
			return 'destructive';
		}
		return 'outline';
	});

	const voucherStatusLabel = $derived(() => {
		const active = user?.voucherActive;
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
						aria-current={item.slug === activeNav ? 'page' : undefined}
					>
						<item.icon />
						<div class="flex min-w-0 flex-col gap-0.5 text-left">
							<span class="truncate text-sm font-medium">{item.title}</span>
						</div>
					</a>
				{/snippet}
				<SidebarMenuItem class="cursor-pointer">
					<SidebarMenuButton
						isActive={item.slug === activeNav}
						tooltipContent={item.title}
						child={NavLink}
					/>
					{@const badgeText = navBadges[item.slug]}
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
				<Popover bind:open={isUserMenuOpen}>
					<PopoverTrigger
						type="button"
						class={cn(
							'cursor-pointer flex w-full min-w-0 items-center gap-3 rounded-md bg-sidebar-accent/60 px-3 py-2 text-left transition hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring focus-visible:outline-none',
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
							<ChevronDown
								class={cn('h-4 w-4 transition-transform duration-200', isUserMenuOpen && 'rotate-180')}
							/>
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
