<script lang="ts">
	import { browser } from '$app/environment';
	import { ContextMenu as ContextMenuPrimitive } from 'bits-ui';
	import {
		ContextMenu,
		ContextMenuContent,
		ContextMenuItem,
		ContextMenuSeparator,
		ContextMenuSub,
		ContextMenuSubContent,
		ContextMenuSubTrigger,
		ContextMenuTrigger
	} from '$lib/components/ui/context-menu/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Tooltip, TooltipContent, TooltipTrigger } from '$lib/components/ui/tooltip/index.js';
	import { TableCell } from '$lib/components/ui/table/index.js';
	import OsLogo from '$lib/components/os-logo.svelte';
	import { cn } from '$lib/utils.js';
	import { toast } from 'svelte-sonner';
	import { countryCodeToFlag } from '$lib/utils/location';
	import type { AgentSnapshot } from '../../../../../../shared/types/agent';
	import type { SectionKey } from '$lib/client-sections';

	type TriggerChildProps = Parameters<NonNullable<ContextMenuPrimitive.TriggerProps['child']>>[0];

	type GeoLookupPayload = {
		countryName: string | null;
		countryCode: string | null;
		isProxy: boolean;
	};

	type ResolvedLocation = {
		label: string;
		flagEmoji: string;
		flagUrl?: string;
		isVpn: boolean;
	};

	let {
		agent,
		openSection,
		openManageTags,
		onTagClick,
		copyAgentId,
		getAgentLocation,
		getAgentTags,
		formatPing,
		formatDate,
		ipLocations,
		layout = 'table'
	} = $props<{
		agent: AgentSnapshot;
		openSection: (section: SectionKey, agent: AgentSnapshot) => void;
		openManageTags: (agent: AgentSnapshot) => void;
		copyAgentId: (agentId: string) => void;
		onTagClick: (tag: string) => void;
		getAgentLocation: (agent: AgentSnapshot) => { label: string; flag: string };
		getAgentTags: (agent: AgentSnapshot) => string[];
		formatPing: (agent: AgentSnapshot) => string;
		formatDate: (value: string) => string;
		ipLocations: Record<string, GeoLookupPayload>;
		layout?: 'table' | 'card';
	}>();

	function toResolvedLocation(base: { label: string; flag: string }): ResolvedLocation {
		return {
			label: base.label,
			flagEmoji: base.flag,
			flagUrl: undefined,
			isVpn: false
		};
	}

	let locationDisplay = $state<ResolvedLocation>(toResolvedLocation(getAgentLocation(agent)));

	$effect(() => {
		const baseLocation = toResolvedLocation(getAgentLocation(agent));
		locationDisplay = baseLocation;

		const normalizedIp = normalizeIp(agent.metadata.publicIpAddress);
		if (!normalizedIp) {
			return;
		}

		const lookup = ipLocations[normalizedIp];
		if (!lookup) {
			return;
		}

		const countryName = lookup.countryName?.trim() || baseLocation.label;
		const countryCode = lookup.countryCode?.trim()?.toUpperCase() ?? '';
		const flagEmoji = countryCode ? countryCodeToFlag(countryCode) : baseLocation.flagEmoji;
		const flagUrl = countryCode
			? `https://flagcdn.com/${countryCode.toLowerCase()}.svg`
			: baseLocation.flagUrl;

		locationDisplay = {
			label: countryName,
			flagEmoji: flagEmoji || baseLocation.flagEmoji,
			flagUrl,
			isVpn: lookup.isProxy === true
		};
	});

	async function handleCopyValue(event: MouseEvent, rawValue: string, label: string) {
		event.stopPropagation();

		const value = rawValue.trim();
		if (!value) {
			toast.error(`No ${label} available to copy`, { position: 'bottom-right' });
			return;
		}

		if (!browser) {
			toast.error('Clipboard unavailable in this environment', { position: 'bottom-right' });
			return;
		}

		const clipboard = navigator.clipboard;
		if (!clipboard?.writeText) {
			toast.error('Clipboard API is not accessible', { position: 'bottom-right' });
			return;
		}

		try {
			await clipboard.writeText(value);
			toast.success(`${label} copied`, {
				description: value,
				position: 'bottom-right'
			});
		} catch (error) {
			console.error(`Failed to copy ${label}`, error);
			toast.error(`Failed to copy ${label}`, { position: 'bottom-right' });
		}
	}

	function buildStatusMeta(agent: AgentSnapshot): {
		label: string;
		className: string;
		indicatorClass: string;
		tooltip: string;
	} {
		const connectedLabel = formatDate(agent.connectedAt);
		const lastSeenLabel = formatDate(agent.lastSeen);
		if (agent.status === 'online') {
			return {
				label: 'Online',
				className: 'text-emerald-500',
				indicatorClass: 'bg-emerald-500',
				tooltip: `Connected since ${connectedLabel}`
			};
		}
		if (agent.status === 'offline') {
			return {
				label: 'Offline',
				className: 'text-muted-foreground',
				indicatorClass: 'bg-muted-foreground',
				tooltip: `Last seen ${lastSeenLabel}`
			};
		}
		const referenceLabel = agent.lastSeen ? lastSeenLabel : connectedLabel;
		return {
			label: 'Error',
			className: 'text-rose-500',
			indicatorClass: 'bg-rose-500',
			tooltip: `Last seen ${referenceLabel}`
		};
	}

	function normalizeIp(rawValue: string | null | undefined): string {
		const raw = (rawValue ?? '').trim();
		if (!raw) {
			return '';
		}

		const withoutBrackets = raw.startsWith('[') && raw.endsWith(']') ? raw.slice(1, -1) : raw;
		return withoutBrackets.toLowerCase();
	}

	const tags = $derived(getAgentTags(agent));
	const statusMeta = $derived(buildStatusMeta(agent));
	const publicIpValue = $derived(agent.metadata.publicIpAddress?.trim() || agent.metadata.ipAddress?.trim() || '');
	const publicIpDisplay = $derived(publicIpValue || 'Unknown');
	const usernameValue = $derived(agent.metadata.username?.trim() ?? '');
	const usernameDisplay = $derived(usernameValue || 'Unknown');
</script>

{#snippet TriggerChild({ props }: TriggerChildProps)}
	{@const { class: providedClass, ...restProps } = (props ?? {}) as {
		class?: string;
		[key: string]: unknown;
	}}
	{@const tableClassName = cn(
		'cursor-context-menu border-b transition-colors hover:bg-muted/50 data-[state=selected]:bg-muted',
		providedClass
	)}
	{@const cardClassName = cn(
		'group cursor-context-menu rounded-lg border border-border/60 bg-background/80 p-4 transition-colors hover:border-border hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background data-[state=selected]:border-ring data-[state=selected]:bg-muted',
		providedClass
	)}
	{#if layout === 'card'}
		<div {...restProps} class={cardClassName} data-slot="table-row" data-mobile="true">
			<div class="flex flex-wrap items-start justify-between gap-3">
				<div class="flex min-w-0 items-center gap-2">
					{#if locationDisplay.flagUrl}
						<img
							src={locationDisplay.flagUrl}
							alt=""
							class="h-4 w-6 shrink-0 rounded-sm border border-border/60 object-cover"
							loading="lazy"
						/>
					{:else}
						<span class="shrink-0 text-xl" aria-hidden="true">{locationDisplay.flagEmoji}</span>
					{/if}
					<div class="min-w-0">
						<p class="truncate text-sm font-medium text-foreground">{locationDisplay.label}</p>
						{#if locationDisplay.isVpn}
							<Tooltip>
								<TooltipTrigger>
									{#snippet child({ props })}
										<span {...props}>
											<Badge
												variant="outline"
												class="mt-1 inline-flex border-amber-500 bg-amber-500/10 px-2 py-0.5 text-[0.65rem] font-medium tracking-wide text-amber-500 uppercase"
											>
												VPN
											</Badge>
										</span>
									{/snippet}
								</TooltipTrigger>
								<TooltipContent side="top" align="center" class="max-w-[18rem] text-xs">
									Flagged as a proxy, VPN, or Tor exit node.
								</TooltipContent>
							</Tooltip>
						{/if}
					</div>
				</div>
				<Tooltip>
					<TooltipTrigger>
						{#snippet child({ props })}
							<span
								{...props}
								class={cn(
									'inline-flex items-center gap-2 rounded-full px-3 py-1 text-sm font-medium',
									statusMeta.className,
									'bg-muted/60'
								)}
							>
								<span
									class={cn('h-2 w-2 rounded-full', statusMeta.indicatorClass)}
									aria-hidden="true"
								></span>
								{statusMeta.label}
							</span>
						{/snippet}
					</TooltipTrigger>
					<TooltipContent side="top" align="end" class="max-w-[18rem] text-xs">
						{statusMeta.tooltip}
					</TooltipContent>
				</Tooltip>
			</div>
			<div class="mt-4 grid gap-3">
				<div class="flex flex-col gap-1">
					<span class="text-xs font-medium tracking-wide text-muted-foreground uppercase"
						>Public IP</span
					>
					<button
						type="button"
						class="inline-flex w-full cursor-pointer items-center justify-between gap-2 rounded-md border border-transparent bg-muted/40 px-3 py-1 text-sm text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background focus-visible:outline-none"
						onclick={(event) => handleCopyValue(event, publicIpValue, 'Public IP')}
						title={publicIpDisplay === 'Unknown'
							? 'Public IP unavailable'
							: `Copy ${publicIpDisplay}`}
						aria-label={publicIpDisplay === 'Unknown'
							? 'Public IP unavailable'
							: `Copy public IP ${publicIpDisplay}`}
					>
						<span class="truncate">{publicIpDisplay}</span>
					</button>
				</div>
				<div class="flex flex-col gap-1">
					<span class="text-xs font-medium tracking-wide text-muted-foreground uppercase"
						>Username</span
					>
					<button
						type="button"
						class="inline-flex w-full cursor-pointer items-center justify-between gap-2 rounded-md border border-transparent bg-muted/40 px-3 py-1 text-sm text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background focus-visible:outline-none"
						onclick={(event) => handleCopyValue(event, usernameValue, 'Username')}
						title={usernameDisplay === 'Unknown'
							? 'Username unavailable'
							: `Copy ${usernameDisplay}`}
						aria-label={usernameDisplay === 'Unknown'
							? 'Username unavailable'
							: `Copy username ${usernameDisplay}`}
					>
						<span class="truncate">{usernameDisplay}</span>
					</button>
				</div>
				<div class="flex flex-col gap-1">
					<span class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Tags</span
					>
					{#if tags.length > 0}
						<div class="flex flex-wrap gap-1">
							{#each tags as tag (tag)}
								<button
									type="button"
									class="group cursor-pointer rounded-md focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background focus-visible:outline-none"
									onclick={(event) => {
										event.stopPropagation();
										onTagClick(tag);
									}}
									aria-label={`Filter by ${tag}`}
								>
									<Badge
										variant="secondary"
										class="px-2 py-0.5 text-xs font-medium transition-colors group-focus-visible:ring-2 group-focus-visible:ring-ring"
									>
										{tag}
									</Badge>
								</button>
							{/each}
						</div>
					{:else}
						<Badge
							variant="outline"
							class="w-fit border-dashed px-2 py-0.5 text-xs text-muted-foreground"
						>
							Untagged
						</Badge>
					{/if}
				</div>
				<div class="grid grid-cols-2 gap-3 text-sm text-muted-foreground">
					<div class="flex flex-col gap-1">
						<span class="text-xs font-medium tracking-wide text-muted-foreground uppercase"
							>Ping</span
						>
						<span>{formatPing(agent)}</span>
					</div>
					<div class="flex flex-col gap-1">
						<span class="text-xs font-medium tracking-wide text-muted-foreground uppercase"
							>Version</span
						>
						<span>{agent.metadata.version ?? 'N/A'}</span>
					</div>
					<div class="flex flex-col gap-1">
						<span class="text-xs font-medium tracking-wide text-muted-foreground uppercase">OS</span
						>
						<span class="flex items-center gap-2 text-foreground">
							<OsLogo os={agent.metadata.os} />
						</span>
					</div>
				</div>
			</div>
		</div>
	{:else}
		<tr {...restProps} class={tableClassName} data-slot="table-row" data-mobile="false">
			<TableCell>
				<div class="flex items-center gap-2">
					{#if locationDisplay.flagUrl}
						<img
							src={locationDisplay.flagUrl}
							alt=""
							class="h-4 w-6 rounded-sm border border-border/60 object-cover"
							loading="lazy"
						/>
					{:else}
						<span class="text-xl" aria-hidden="true">{locationDisplay.flagEmoji}</span>
					{/if}
					<span class="text-sm font-medium text-foreground">{locationDisplay.label}</span>
					{#if locationDisplay.isVpn}
						<Tooltip>
							<TooltipTrigger>
								{#snippet child({ props })}
									<span {...props}>
										<Badge
											variant="outline"
											class="border-amber-500 bg-amber-500/10 text-amber-500"
										>
											VPN
										</Badge>
									</span>
								{/snippet}
							</TooltipTrigger>
							<TooltipContent side="top" align="center" class="max-w-[18rem] text-xs">
								Flagged as a proxy, VPN, or Tor exit node.
							</TooltipContent>
						</Tooltip>
					{/if}
				</div>
			</TableCell>
			<TableCell class="text-center">
				<button
					type="button"
					class="inline-flex w-full cursor-pointer items-center justify-center truncate rounded-md px-2 py-1 text-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background focus-visible:outline-none"
					onclick={(event) => handleCopyValue(event, publicIpValue, 'Public IP')}
					title={publicIpDisplay === 'Unknown'
						? 'Public IP unavailable'
						: `Copy ${publicIpDisplay}`}
					aria-label={publicIpDisplay === 'Unknown'
						? 'Public IP unavailable'
						: `Copy public IP ${publicIpDisplay}`}
				>
					<span class="truncate">{publicIpDisplay}</span>
				</button>
			</TableCell>
			<TableCell class="text-center">
				<button
					type="button"
					class="inline-flex w-full cursor-pointer items-center justify-center truncate rounded-md px-2 py-1 text-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background focus-visible:outline-none"
					onclick={(event) => handleCopyValue(event, usernameValue, 'Username')}
					title={usernameDisplay === 'Unknown' ? 'Username unavailable' : `Copy ${usernameDisplay}`}
					aria-label={usernameDisplay === 'Unknown'
						? 'Username unavailable'
						: `Copy username ${usernameDisplay}`}
				>
					<span class="truncate">{usernameDisplay}</span>
				</button>
			</TableCell>
			<TableCell class="text-center">
				{#if tags.length > 0}
					<div class="flex flex-wrap items-center justify-center gap-1">
						{#each tags as tag (tag)}
							<button
								type="button"
								class="group cursor-pointer rounded-md focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background focus-visible:outline-none"
								onclick={(event) => {
									event.stopPropagation();
									onTagClick(tag);
								}}
								aria-label={`Filter by ${tag}`}
							>
								<Badge
									variant="secondary"
									class="px-2 py-0.5 text-xs font-medium transition-colors group-focus-visible:ring-2 group-focus-visible:ring-ring"
								>
									{tag}
								</Badge>
							</button>
						{/each}
					</div>
				{:else}
					<Badge variant="outline" class="border-dashed px-2 py-0.5 text-xs text-muted-foreground">
						Untagged
					</Badge>
				{/if}
			</TableCell>
			<TableCell class="text-center">
				<OsLogo os={agent.metadata.os} />
			</TableCell>
			<TableCell class="text-center text-sm text-muted-foreground">
				{formatPing(agent)}
			</TableCell>
			<TableCell class="text-center text-sm text-muted-foreground">
				{agent.metadata.version ?? 'N/A'}
			</TableCell>
			<TableCell class="text-center">
				<Tooltip>
					<TooltipTrigger>
						{#snippet child({ props })}
							<span
								{...props}
								class={cn(
									'inline-flex w-full items-center justify-center gap-2 text-sm font-medium',
									statusMeta.className
								)}
							>
								<span
									class={cn('h-2 w-2 rounded-full', statusMeta.indicatorClass)}
									aria-hidden="true"
								></span>
								{statusMeta.label}
							</span>
						{/snippet}
					</TooltipTrigger>
					<TooltipContent side="top" align="center" class="max-w-[18rem] text-xs">
						{statusMeta.tooltip}
					</TooltipContent>
				</Tooltip>
			</TableCell>
		</tr>
	{/if}
{/snippet}
<ContextMenu>
	<ContextMenuTrigger child={TriggerChild} />
	<ContextMenuContent class="w-56">
		<ContextMenuItem onSelect={() => openSection('systemInfo', agent)}>System Info</ContextMenuItem
		>
		<ContextMenuItem onSelect={() => openSection('notes', agent)}>Notes</ContextMenuItem>
		<ContextMenuItem onSelect={() => openManageTags(agent)}>Manage Tags</ContextMenuItem>

		<ContextMenuSeparator />

		<ContextMenuSub>
			<ContextMenuSubTrigger>Control</ContextMenuSubTrigger>
			<ContextMenuSubContent class="w-48">
				<ContextMenuItem onSelect={() => openSection('appVnc', agent)}>App VNC</ContextMenuItem>
				<ContextMenuItem onSelect={() => openSection('remoteDesktop', agent)}
					>Remote Desktop</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openSection('webcamControl', agent)}
					>Webcam Control</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openSection('audioControl', agent)}
					>Audio Control</ContextMenuItem
				>
				<ContextMenuSub>
					<ContextMenuSubTrigger>Keylogger</ContextMenuSubTrigger>
					<ContextMenuSubContent class="w-48">
						<ContextMenuItem onSelect={() => openSection('keyloggerStandard', agent)}
							>Standard</ContextMenuItem
						>
						<ContextMenuItem onSelect={() => openSection('keyloggerOffline', agent)}
							>Offline</ContextMenuItem
						>
					</ContextMenuSubContent>
				</ContextMenuSub>
				<ContextMenuItem onSelect={() => openSection('cmd', agent)}>CMD</ContextMenuItem>
			</ContextMenuSubContent>
		</ContextMenuSub>

		<ContextMenuSeparator />

		<ContextMenuSub>
			<ContextMenuSubTrigger>Management</ContextMenuSubTrigger>
			<ContextMenuSubContent class="w-48">
				<ContextMenuItem onSelect={() => openSection('fileManager', agent)}
					>File Manager</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openSection('systemMonitor', agent)}
					>System Monitor</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openSection('registryManager', agent)}
					>Registry Manager</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openSection('clipboardManager', agent)}
					>Clipboard Manager</ContextMenuItem
				>
			</ContextMenuSubContent>
		</ContextMenuSub>

		<ContextMenuSeparator />

		<ContextMenuItem onSelect={() => openSection('recovery', agent)}>Recovery</ContextMenuItem>
		<ContextMenuItem onSelect={() => openSection('options', agent)}>Options</ContextMenuItem>

		<ContextMenuSeparator />

		<ContextMenuSub>
			<ContextMenuSubTrigger>Miscellaneous</ContextMenuSubTrigger>
			<ContextMenuSubContent class="w-48">
				<ContextMenuItem onSelect={() => openSection('openUrl', agent)}>Open URL</ContextMenuItem>
				<ContextMenuItem onSelect={() => openSection('clientChat', agent)}
					>Client Chat</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openSection('triggerMonitor', agent)}
					>Trigger Monitor</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openSection('ipGeolocation', agent)}
					>IP Geolocation</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openSection('environmentVariables', agent)}
					>Environment Variables</ContextMenuItem
				>
			</ContextMenuSubContent>
		</ContextMenuSub>

		<ContextMenuSeparator />

		<ContextMenuSub>
			<ContextMenuSubTrigger>System Controls</ContextMenuSubTrigger>
			<ContextMenuSubContent class="w-48">
				<ContextMenuItem onSelect={() => openSection('reconnect', agent)}
					>Reconnect</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openSection('disconnect', agent)}
					>Disconnect</ContextMenuItem
				>
			</ContextMenuSubContent>
		</ContextMenuSub>

		<ContextMenuSeparator />

		<ContextMenuSub>
			<ContextMenuSubTrigger>Power</ContextMenuSubTrigger>
			<ContextMenuSubContent class="w-48">
				<ContextMenuItem onSelect={() => openSection('shutdown', agent)}>Shutdown</ContextMenuItem>
				<ContextMenuItem onSelect={() => openSection('restart', agent)}>Restart</ContextMenuItem>
				<ContextMenuItem onSelect={() => openSection('sleep', agent)}>Sleep</ContextMenuItem>
				<ContextMenuItem onSelect={() => openSection('logoff', agent)}>Logoff</ContextMenuItem>
			</ContextMenuSubContent>
		</ContextMenuSub>

		<ContextMenuSeparator />

		<ContextMenuItem onSelect={() => copyAgentId(agent.id)}>Copy agent ID</ContextMenuItem>
	</ContextMenuContent>
</ContextMenu>
