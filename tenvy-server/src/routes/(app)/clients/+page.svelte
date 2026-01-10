<script lang="ts">
	import { SvelteSet } from 'svelte/reactivity';
	import { browser } from '$app/environment';
	import { goto, invalidate } from '$app/navigation';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import {
		Tooltip,
		TooltipContent,
		TooltipProvider,
		TooltipTrigger
	} from '$lib/components/ui/tooltip/index.js';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table/index.js';
	import {
		Select,
		SelectContent,
		SelectItem,
		SelectTrigger
	} from '$lib/components/ui/select/index.js';
	import ClientToolDialog from '$lib/components/client-tool-dialog.svelte';
	import ClientsTableRow from './components/clients-table-row.svelte';
	import ManageTagsDialog from './components/manage-tags-dialog.svelte';
	import DeployAgentDialog from './components/deploy-agent-dialog.svelte';
	import { sectionToolMap, type SectionKey } from '$lib/client-sections';
	import { createClientsTableStore } from '$lib/stores/clients-table';
	import {
		buildClientToolUrl,
		getClientTool,
		isDialogTool,
		type ClientToolId,
		type DialogToolId
	} from '$lib/data/client-tools';
	import { isWorkspaceTool } from '$lib/data/client-tool-workspaces';
	import { buildLocationDisplay } from '$lib/utils/location';
	import { isLikelyPrivateIp } from '$lib/utils/ip';
	import { formatAgentLatency } from '$lib/utils/agent-latency';
	import { toast } from 'svelte-sonner';
	import type { Client } from '$lib/data/clients';
	import { get, writable } from 'svelte/store';
	import { onDestroy } from 'svelte';
	import ChevronLeft from '@lucide/svelte/icons/chevron-left';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import Search from '@lucide/svelte/icons/search';
	import AlertCircle from '@lucide/svelte/icons/alert-circle';
	import CheckCircle2 from '@lucide/svelte/icons/check-circle-2';
	import Info from '@lucide/svelte/icons/info';
	import type {
		AgentConnectionAction,
		AgentConnectionRequest,
		AgentSnapshot,
		AgentTagUpdateResponse
	} from '../../../../../shared/types/agent';
	import type {
		AgentControlCommandPayload,
		CommandDeliveryMode,
		CommandInput,
		CommandQueueResponse
	} from '../../../../../shared/types/messages';

	const statusLabels: Record<AgentSnapshot['status'], string> = {
		online: 'Online',
		offline: 'Offline',
		error: 'Error'
	};

	type PowerAction = Extract<
		AgentControlCommandPayload['action'],
		'shutdown' | 'restart' | 'sleep' | 'logoff'
	>;

	const powerToolIds = new Set<ClientToolId>(['shutdown', 'restart', 'sleep', 'logoff']);

	const powerActionMeta: Record<PowerAction, { label: string; noun: string }> = {
		shutdown: { label: 'Shutdown', noun: 'shutdown' },
		restart: { label: 'Restart', noun: 'restart' },
		sleep: { label: 'Sleep', noun: 'sleep' },
		logoff: { label: 'Logoff', noun: 'log off' }
	};

	const relativeTimeFormatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });

	const desktopMediaQuery = '(min-width: 768px)';
	let isDesktop = $state(false);

	if (browser) {
		const mediaQuery = window.matchMedia(desktopMediaQuery);
		const applyMatch = (matches: boolean) => {
			isDesktop = matches;
		};

		applyMatch(mediaQuery.matches);

		const handleChange = (event: MediaQueryListEvent) => {
			applyMatch(event.matches);
		};

		mediaQuery.addEventListener('change', handleChange);

		onDestroy(() => {
			mediaQuery.removeEventListener('change', handleChange);
		});
	}

	let { data } = $props<{ data: { agents: AgentSnapshot[] } }>();

	const clientsTable = createClientsTableStore(data.agents ?? []);
	const ipLocationStore = writable<Record<string, GeoLookupPayload>>({});
	const inFlightLookups = new SvelteSet<string>();

	$effect(() => {
		clientsTable.setAgents(data.agents ?? []);
	});

	const perPageOptions = [10, 25, 50];

	const clientTableColumnWidths = [
		'16rem',
		'6rem',
		'6rem',
		'12rem',
		'5rem',
		'6rem',
		'6rem',
		'7rem'
	];

	type GeoLookupPayload = {
		countryName: string | null;
		countryCode: string | null;
		isProxy: boolean;
	};

	let toolDialog = $state<{ agentId: string; toolId: DialogToolId } | null>(null);
	let toolDialogAgent = $state<AgentSnapshot | null | undefined>(undefined);
	let toolDialogClient = $state<Client | null>(null);

	type PageAlertVariant = 'default' | 'destructive';

	type PageAlert = {
		title: string;
		description: string;
		variant: PageAlertVariant;
	};

	let connectionAlert = $state<PageAlert | null>(null);

	$effect(() => {
		if (toolDialog && toolDialogAgent === null) {
			toolDialog = null;
		}
	});

	$effect(() => {
		const agentId = toolDialog?.agentId ?? null;
		const agents = $clientsTable.agents;
		const agent =
			agentId !== null ? (agents.find((item) => item.id === agentId) ?? null) : undefined;
		toolDialogAgent = agent;
		toolDialogClient = agent ? mapAgentToClient(agent) : null;
	});

	let commandErrors = $state<Record<string, string | null>>({});
	let commandSuccess = $state<Record<string, string | null>>({});
	let commandPending = $state<Record<string, boolean>>({});
	let deployDialogOpen = $state(false);
	let tagsDialogAgentId = $state<string | null>(null);
	let tagsAgent = $state<AgentSnapshot | null>(null);
	let tagsDialogPending = $state(false);
	let tagsDialogError = $state<string | null>(null);

	$effect(() => {
		const agentId = tagsDialogAgentId;
		const agents = $clientsTable.agents;
		tagsAgent = agentId ? (agents.find((agent) => agent.id === agentId) ?? null) : null;
	});

	type CopyFeedback = { message: string; variant: 'success' | 'error' } | null;
	let copyFeedback = $state<CopyFeedback>(null);
	let copyFeedbackTimeout: number | undefined;
	let connectionAlertTimeout: number | undefined;

	function updateRecord<T>(records: Record<string, T>, key: string, value: T): Record<string, T> {
		return { ...records, [key]: value };
	}

	function hasActiveConnection(agent: AgentSnapshot): boolean {
		return agent.status === 'online';
	}

	function showConnectionAlert(alert: PageAlert) {
		connectionAlert = alert;

		if (!browser) {
			return;
		}

		if (connectionAlertTimeout !== undefined) {
			window.clearTimeout(connectionAlertTimeout);
		}

		connectionAlertTimeout = window.setTimeout(() => {
			connectionAlert = null;
			connectionAlertTimeout = undefined;
		}, 4000);
	}

	function formatDate(value: string): string {
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) {
			return 'Unknown';
		}
		return date.toLocaleString();
	}

	function formatRelative(value: string): string {
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) {
			return 'unknown';
		}
		const diffMs = Date.now() - date.getTime();
		if (diffMs <= 0) {
			return 'just now';
		}
		const diffSeconds = Math.floor(diffMs / 1000);
		const units: [Intl.RelativeTimeFormatUnit, number][] = [
			['day', 86_400],
			['hour', 3_600],
			['minute', 60],
			['second', 1]
		];
		for (const [unit, seconds] of units) {
			if (diffSeconds >= seconds || unit === 'second') {
				const value = Math.floor(diffSeconds / seconds) * -1;
				return relativeTimeFormatter.format(value, unit);
			}
		}
		return 'just now';
	}

	function normalizeIpAddress(rawValue: string | null | undefined): string {
		const raw = (rawValue ?? '').trim();
		if (!raw) {
			return '';
		}

		const withoutBrackets = raw.startsWith('[') && raw.endsWith(']') ? raw.slice(1, -1) : raw;
		return withoutBrackets.toLowerCase();
	}

	async function fetchGeoLocations(ips: string[]): Promise<void> {
		if (!browser || ips.length === 0) {
			for (const ip of ips) {
				inFlightLookups.delete(ip);
			}
			return;
		}

		try {
			const response = await fetch('/api/geo', {
				method: 'POST',
				headers: {
					Accept: 'application/json',
					'Content-Type': 'application/json'
				},
				body: JSON.stringify(ips)
			});

			if (response.ok) {
				const payload = (await response.json()) as Record<string, GeoLookupPayload>;
				ipLocationStore.update((current) => ({ ...current, ...payload }));
			}
		} catch (err) {
			console.error('Failed to fetch geo locations', err);
		} finally {
			for (const ip of ips) {
				inFlightLookups.delete(ip);
			}
		}
	}

	$effect(() => {
		if (!browser) {
			return;
		}

		const agents = $clientsTable.agents;
		const knownLookups = $ipLocationStore;
		const pending = new Set<string>();

		for (const agent of agents) {
			const normalized = normalizeIpAddress(agent.metadata.publicIpAddress);
			if (!normalized || isLikelyPrivateIp(normalized)) {
				continue;
			}

			if (knownLookups[normalized] || inFlightLookups.has(normalized)) {
				continue;
			}

			pending.add(normalized);
		}

		if (pending.size === 0) {
			return;
		}

		for (const ip of pending) {
			inFlightLookups.add(ip);
		}

		void fetchGeoLocations(Array.from(pending));
	});

	function getAgentLocation(agent: AgentSnapshot): { label: string; flag: string } {
		return buildLocationDisplay(agent.metadata.location);
	}

	function getAgentTags(agent: AgentSnapshot): string[] {
		const tags =
			agent.metadata.tags
				?.map((tag) => tag.trim())
				.filter((tag): tag is string => tag.length > 0) ?? [];

		const uniqueTags = Array.from(new Set(tags));
		if (uniqueTags.length > 0) {
			return uniqueTags;
		}

		const fallback = agent.metadata.group?.trim();
		return fallback ? [fallback] : [];
	}

	function formatPing(agent: AgentSnapshot): string {
		return formatAgentLatency(agent);
	}

	function handleTagFilter(tag: string) {
		if (!tag || tag.trim().length === 0) {
			clientsTable.setTagFilter('all');
			return;
		}
		clientsTable.setTagFilter(tag);
	}

	function openManageTagsDialog(agent: AgentSnapshot) {
		tagsDialogAgentId = agent.id;
		tagsDialogError = null;
		tagsDialogPending = false;
	}

	function closeManageTagsDialog() {
		if (tagsDialogPending) {
			return;
		}
		tagsDialogAgentId = null;
		tagsAgent = null;
		tagsDialogError = null;
	}

	async function handleTagsSubmit(event: CustomEvent<{ tags: string[] }>) {
		const targetAgent = tagsAgent;
		if (!targetAgent) {
			return;
		}

		const tags = event.detail?.tags ?? [];
		tagsDialogPending = true;
		tagsDialogError = null;

		try {
			const response = await fetch(`/api/agents/${targetAgent.id}/tags`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ tags })
			});

			if (!response.ok) {
				const message = (await response.text()) || 'Failed to update tags';
				throw new Error(message);
			}

			const payload = (await response.json()) as AgentTagUpdateResponse;
			const updatedAgent = payload.agent;

			const current = get(clientsTable).agents;
			clientsTable.setAgents(
				current.map((item) => (item.id === updatedAgent.id ? updatedAgent : item))
			);

			await invalidate('/api/agents');

			tagsDialogAgentId = null;
			tagsAgent = null;
		} catch (err) {
			console.error('Failed to update agent tags', err);
			tagsDialogError = err instanceof Error && err.message ? err.message : 'Failed to update tags';
		} finally {
			tagsDialogPending = false;
		}
	}

	async function requestConnectionAction(
		agent: AgentSnapshot,
		action: AgentConnectionAction
	): Promise<boolean> {
		const agentLabel = agent.metadata.hostname?.trim() || agent.id;

		try {
			const response = await fetch(`/api/agents/${agent.id}/connection`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ action } satisfies AgentConnectionRequest)
			});

			if (!response.ok) {
				const message = (await response.text()) || 'Unable to update connection';
				showConnectionAlert({
					title: 'Connection request failed',
					description: message.trim(),
					variant: 'destructive'
				});
				return false;
			}

			await invalidate('/api/agents');

			const titles: Record<AgentConnectionAction, string> = {
				disconnect: 'Agent disconnected',
				reconnect: 'Agent reconnected'
			};

			const descriptions: Record<AgentConnectionAction, string> = {
				disconnect: `${agentLabel} is now disconnected from the controller.`,
				reconnect: `${agentLabel} has been reconnected to the controller.`
			};

			showConnectionAlert({
				title: titles[action],
				description: descriptions[action],
				variant: 'default'
			});

			return true;
		} catch (err) {
			showConnectionAlert({
				title: 'Connection request failed',
				description: err instanceof Error ? err.message : 'Unable to update connection',
				variant: 'destructive'
			});
			return false;
		}
	}

	async function requestPowerAction(agent: AgentSnapshot, action: PowerAction): Promise<boolean> {
		if (!browser) {
			return false;
		}

		const { label, noun } = powerActionMeta[action];
		const agentLabel = agent.metadata.hostname?.trim() || agent.id;

		if (!hasActiveConnection(agent)) {
			toast.error(`${label} unavailable`, {
				description: `${agentLabel} is not currently connected.`,
				position: 'bottom-right'
			});
			return false;
		}

		const request: CommandInput = {
			name: 'agent-control',
			payload: {
				action,
				force: true
			} satisfies AgentControlCommandPayload
		};

		try {
			const response = await fetch(`/api/agents/${agent.id}/commands`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(request)
			});

			if (!response.ok) {
				const detail = (await response.text().catch(() => ''))?.trim();
				toast.error(`${label} failed`, {
					description: detail || 'Failed to queue command.',
					position: 'bottom-right'
				});
				return false;
			}

			const payload = (await response.json().catch(() => null)) as CommandQueueResponse | null;
			const delivery: CommandDeliveryMode = payload?.delivery ?? 'queued';
			const description =
				delivery === 'session'
					? `${agentLabel} received the ${noun} command immediately.`
					: `Forced ${noun} command queued for ${agentLabel}.`;

			await invalidate('/api/agents');

			toast.success(`${label} command sent`, {
				description,
				position: 'bottom-right'
			});

			return true;
		} catch (err) {
			const message = err instanceof Error ? err.message : 'Failed to queue command.';
			toast.error(`${label} failed`, {
				description: message,
				position: 'bottom-right'
			});
			return false;
		}
	}

	function sendCommandError(agent: AgentSnapshot, action: string) {
		const agentLabel = agent.metadata.hostname?.trim() || agent.id;
		showConnectionAlert({
			title: 'Connection unavailable',
			description: `${agentLabel} is not currently connected. Re-establish the session to ${action}.`,
			variant: 'destructive'
		});
	}

	async function disconnectAgent(agent: AgentSnapshot) {
		if (!hasActiveConnection(agent)) {
			sendCommandError(agent, 'disconnect the agent');
			return;
		}

		await requestConnectionAction(agent, 'disconnect');
	}

	async function reconnectAgent(agent: AgentSnapshot) {
		await requestConnectionAction(agent, 'reconnect');
	}

	function setCopyFeedback(feedback: CopyFeedback) {
		copyFeedback = feedback;
		if (!browser) {
			return;
		}
		if (copyFeedbackTimeout !== undefined) {
			window.clearTimeout(copyFeedbackTimeout);
		}
		if (feedback) {
			copyFeedbackTimeout = window.setTimeout(() => {
				copyFeedback = null;
				copyFeedbackTimeout = undefined;
			}, 2500);
		}
	}

	async function copyAgentId(agentId: string) {
		if (!browser) return;
		try {
			await navigator.clipboard.writeText(agentId);
			setCopyFeedback({ message: 'Agent ID copied to clipboard', variant: 'success' });
		} catch (err) {
			console.error(err);
			setCopyFeedback({ message: 'Unable to copy agent ID', variant: 'error' });
		}
	}

	onDestroy(() => {
		if (!browser) {
			return;
		}

		if (copyFeedbackTimeout !== undefined) {
			window.clearTimeout(copyFeedbackTimeout);
			copyFeedbackTimeout = undefined;
		}

		if (connectionAlertTimeout !== undefined) {
			window.clearTimeout(connectionAlertTimeout);
			connectionAlertTimeout = undefined;
		}
	});

	function inferClientPlatform(os: string): Client['platform'] {
		const normalized = os.toLowerCase();
		if (normalized.includes('mac')) {
			return 'macos';
		}
		if (normalized.includes('win')) {
			return 'windows';
		}
		return 'linux';
	}

	function mapAgentStatusToClientStatus(status: AgentSnapshot['status']): Client['status'] {
		if (status === 'online') {
			return 'online';
		}
		if (status === 'offline') {
			return 'offline';
		}
		return 'idle';
	}

	function determineClientRisk(status: AgentSnapshot['status']): Client['risk'] {
		return status === 'error' ? 'High' : 'Medium';
	}

	function mapAgentToClient(agent: AgentSnapshot): Client {
		const tags = agent.metadata.tags ?? [];
		const operatorNote = agent.operatorNote;

		return {
			id: agent.id,
			codename: agent.metadata.hostname?.toUpperCase() ?? agent.id.toUpperCase(),
			hostname: agent.metadata.hostname,
			ip: agent.metadata.publicIpAddress ?? agent.metadata.ipAddress ?? 'Unknown',
			location: getAgentLocation(agent).label,
			os: agent.metadata.os,
			platform: inferClientPlatform(agent.metadata.os),
			version: agent.metadata.version ?? 'Unknown',
			status: mapAgentStatusToClientStatus(agent.status),
			lastSeen: formatRelative(agent.lastSeen),
			tags,
			risk: determineClientRisk(agent.status),
			notes: operatorNote?.note ?? '',
			noteTags: operatorNote?.tags ?? [],
			noteUpdatedAt: operatorNote?.updatedAt ?? null,
			noteUpdatedBy: operatorNote?.updatedBy ?? null
		};
	}

	function openSection(section: SectionKey, agent: AgentSnapshot) {
		const toolId = sectionToolMap[section];
		if (!toolId) {
			return;
		}

		if (toolId === 'reconnect') {
			toolDialog = null;
			void reconnectAgent(agent);
			return;
		}

		if (toolId === 'disconnect') {
			toolDialog = null;
			void disconnectAgent(agent);
			return;
		}

		if (powerToolIds.has(toolId)) {
			toolDialog = null;
			void requestPowerAction(agent, toolId as PowerAction);
			return;
		}

		if (!hasActiveConnection(agent)) {
			const agentLabel = agent.metadata.hostname?.trim() || agent.id;
			showConnectionAlert({
				title: 'Connection unavailable',
				description: `${agentLabel} is not currently connected. Re-establish the session to access agent tools.`,
				variant: 'destructive'
			});
			return;
		}

		const tool = getClientTool(toolId);
		const target = tool.target ?? '_blank';

		toolDialog = null;

		if (isWorkspaceTool(toolId)) {
			if (!browser) {
				return;
			}
			const url = buildClientToolUrl(agent.id, tool);
			goto(url as any);
			return;
		}

		if (target === 'dialog' && isDialogTool(toolId)) {
			toolDialog = { agentId: agent.id, toolId };
			return;
		}

		const url = buildClientToolUrl(agent.id, tool);

		if (!browser) {
			return;
		}

		if (target === '_self') {
			goto(url as any);
			return;
		}

		const resolvedUrl = resolve(url as any);

		if (target === '_blank') {
			const newWindow = window.open(resolvedUrl, '_blank', 'noopener');

			if (!newWindow) {
				console.warn('Pop-up blocked when attempting to open client tool in a new tab.');
				return;
			}

			newWindow.opener = null;
			newWindow.focus?.();
			return;
		}

		window.open(resolvedUrl, target, 'noopener');
	}
</script>

<svelte:head>
	<title>Clients · Tenvy</title>
</svelte:head>

<section class="space-y-6">
	<div class="space-y-4 rounded-lg border border-border/60 bg-background/60 p-4">
		<div class="grid gap-4 lg:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)] lg:items-end">
			<div class="flex flex-col gap-2">
				<Label for="client-search" class="text-sm font-medium">Search</Label>
				<div class="relative">
					<Search
						class="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
					/>
					<Input
						id="client-search"
						placeholder="Hostname, user, ID, IP, or tag"
						value={$clientsTable.searchQuery}
						oninput={(event) => clientsTable.setSearchQuery(event.currentTarget.value)}
						class="pl-10"
					/>
				</div>
			</div>
			<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
				<div class="flex flex-col gap-2">
					<Label for="client-status-filter" class="text-sm font-medium">Status</Label>
					<Select
						type="single"
						value={$clientsTable.statusFilter}
						onValueChange={(value) =>
							clientsTable.setStatusFilter(value as 'all' | AgentSnapshot['status'])}
					>
						<SelectTrigger id="client-status-filter" class="w-full">
							<span class="truncate">
								{$clientsTable.statusFilter === 'all'
									? 'All statuses'
									: statusLabels[$clientsTable.statusFilter]}
							</span>
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="all">All statuses</SelectItem>
							<SelectItem value="online">Online</SelectItem>
							<SelectItem value="offline">Offline</SelectItem>
							<SelectItem value="error">Error</SelectItem>
						</SelectContent>
					</Select>
				</div>
				<div class="flex flex-col gap-2">
					<Label for="client-tag-filter" class="text-sm font-medium">Tag</Label>
					<Select
						type="single"
						value={$clientsTable.tagFilter}
						onValueChange={(value) => clientsTable.setTagFilter(value === 'all' ? 'all' : value)}
					>
						<SelectTrigger id="client-tag-filter" class="w-full">
							<span class="truncate">
								{$clientsTable.tagFilter === 'all' ? 'All tags' : $clientsTable.tagFilter}
							</span>
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="all">All tags</SelectItem>
							{#if $clientsTable.availableTags.length > 0}
								{#each $clientsTable.availableTags as tag (tag)}
									<SelectItem value={tag}>{tag}</SelectItem>
								{/each}
							{:else}
								<SelectItem value="all" disabled>No tags available</SelectItem>
							{/if}
						</SelectContent>
					</Select>
				</div>
				<div class="flex flex-col gap-2">
					<Label for="client-page-size" class="text-sm font-medium">Per page</Label>
					<Select
						type="single"
						value={$clientsTable.perPage.toString()}
						onValueChange={(value) => {
							const next = Number.parseInt(value, 10);
							clientsTable.setPerPage(Number.isNaN(next) ? perPageOptions[0] : next);
						}}
					>
						<SelectTrigger id="client-page-size" class="w-full">
							<span>{$clientsTable.perPage} rows</span>
						</SelectTrigger>
						<SelectContent>
							{#each perPageOptions as option (option)}
								<SelectItem value={option.toString()}>{option} rows</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</div>
			</div>
		</div>
		{#if connectionAlert}
			<Alert variant={connectionAlert.variant}>
				<AlertCircle class="h-4 w-4" />
				<AlertTitle>{connectionAlert.title}</AlertTitle>
				<AlertDescription>{connectionAlert.description}</AlertDescription>
			</Alert>
		{/if}

		{#if copyFeedback}
			<Alert
				variant={copyFeedback.variant === 'error' ? 'destructive' : 'default'}
				class={copyFeedback.variant === 'success'
					? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-500'
					: undefined}
			>
				{#if copyFeedback.variant === 'error'}
					<AlertCircle class="h-4 w-4" />
					<AlertTitle>Copy failed</AlertTitle>
				{:else}
					<CheckCircle2 class="h-4 w-4" />
					<AlertTitle>Copied</AlertTitle>
				{/if}
				<AlertDescription>{copyFeedback.message}</AlertDescription>
			</Alert>
		{/if}
	</div>

	<div class="space-y-4">
		<TooltipProvider delayDuration={100}>
			{#if isDesktop}
				<ScrollArea class="rounded-lg border border-border/60">
					<div class="min-w-0 md:min-w-[clamp(48rem,80vw,64rem)] xl:min-w-280">
						<Table>
							<colgroup>
								{#each clientTableColumnWidths as width}
									<col style={`width:${width};`} />
								{/each}
							</colgroup>
							<TableHeader>
								<TableRow>
									<TableHead class="w-[16rem]">
										<Tooltip>
											<TooltipTrigger>
												{#snippet child({ props })}
													<span {...props} class="inline-flex cursor-help items-center gap-1">
														Location
														<Info class="size-3 text-muted-foreground" aria-hidden="true" />
													</span>
												{/snippet}
											</TooltipTrigger>
											<TooltipContent side="top" align="center" class="max-w-[18rem] text-xs">
												Approximate location derived from the agent&apos;s reported metadata and IP
												geolocation.
											</TooltipContent>
										</Tooltip>
									</TableHead>
									<TableHead class="w-24 text-center">
										<Tooltip>
											<TooltipTrigger>
												{#snippet child({ props })}
													<span {...props} class="inline-flex cursor-help items-center gap-1">
														Public IP
														<Info class="size-3 text-muted-foreground" aria-hidden="true" />
													</span>
												{/snippet}
											</TooltipTrigger>
											<TooltipContent side="top" align="center" class="max-w-[18rem] text-xs">
												Public-facing IP address reported by the agent during its latest check-in.
											</TooltipContent>
										</Tooltip>
									</TableHead>
									<TableHead class="w-24 text-center">
										<Tooltip>
											<TooltipTrigger>
												{#snippet child({ props })}
													<span {...props} class="inline-flex cursor-help items-center gap-1">
														Username
														<Info class="size-3 text-muted-foreground" aria-hidden="true" />
													</span>
												{/snippet}
											</TooltipTrigger>
											<TooltipContent side="top" align="center" class="max-w-[18rem] text-xs">
												Logged-in user account reported by the agent.
											</TooltipContent>
										</Tooltip>
									</TableHead>
									<TableHead class="w-48 text-center">
										<Tooltip>
											<TooltipTrigger>
												{#snippet child({ props })}
													<span {...props} class="inline-flex cursor-help items-center gap-1">
														Tags
														<Info class="size-3 text-muted-foreground" aria-hidden="true" />
													</span>
												{/snippet}
											</TooltipTrigger>
											<TooltipContent side="top" align="center" class="max-w-[18rem] text-xs">
												Operator-defined tags applied to this agent.
											</TooltipContent>
										</Tooltip>
									</TableHead>
									<TableHead class="w-20 text-center">
										<Tooltip>
											<TooltipTrigger>
												{#snippet child({ props })}
													<span
														{...props}
														class="inline-flex w-full cursor-help items-center justify-center gap-1"
													>
														OS
														<Info class="size-3 text-muted-foreground" aria-hidden="true" />
													</span>
												{/snippet}
											</TooltipTrigger>
											<TooltipContent side="top" align="center" class="max-w-[18rem] text-xs">
												Operating system detected on the agent.
											</TooltipContent>
										</Tooltip>
									</TableHead>
									<TableHead class="w-24 text-center">
										<Tooltip>
											<TooltipTrigger>
												{#snippet child({ props })}
													<span {...props} class="inline-flex cursor-help items-center gap-1">
														Ping
														<Info class="size-3 text-muted-foreground" aria-hidden="true" />
													</span>
												{/snippet}
											</TooltipTrigger>
											<TooltipContent side="top" align="center" class="max-w-[18rem] text-xs">
												Latest round-trip latency reported by the agent during sync. Displays N/A
												when the agent has not provided latency metrics.
											</TooltipContent>
										</Tooltip>
									</TableHead>
									<TableHead class="w-24 text-center">
										<Tooltip>
											<TooltipTrigger>
												{#snippet child({ props })}
													<span {...props} class="inline-flex cursor-help items-center gap-1">
														Version
														<Info class="size-3 text-muted-foreground" aria-hidden="true" />
													</span>
												{/snippet}
											</TooltipTrigger>
											<TooltipContent side="top" align="center" class="max-w-[18rem] text-xs">
												Tenvy agent build version currently running on the endpoint.
											</TooltipContent>
										</Tooltip>
									</TableHead>
									<TableHead class="w-28 text-center">
										<Tooltip>
											<TooltipTrigger>
												{#snippet child({ props })}
													<span
														{...props}
														class="inline-flex w-full cursor-help items-center justify-center gap-1"
													>
														Status
														<Info class="size-3 text-muted-foreground" aria-hidden="true" />
													</span>
												{/snippet}
											</TooltipTrigger>
											<TooltipContent side="top" align="center" class="max-w-[18rem] text-xs">
												Current connection state reported by the agent.
											</TooltipContent>
										</Tooltip>
									</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{#if $clientsTable.paginatedAgents.length === 0}
									<TableRow>
										<TableCell colspan={8} class="py-12 text-center text-sm text-muted-foreground">
											{#if $clientsTable.agents.length === 0}
												No agents connected yet.
												<Button
													type="button"
													class="ml-2"
													onclick={() => (deployDialogOpen = true)}
												>
													View deployment guide
												</Button>
											{:else}
												No agents match your current filters.
											{/if}
										</TableCell>
									</TableRow>
								{:else}
									{#each $clientsTable.paginatedAgents as agent (agent.id)}
										<ClientsTableRow
											{agent}
											{formatDate}
											{formatPing}
											{getAgentTags}
											{getAgentLocation}
											ipLocations={$ipLocationStore}
											openManageTags={openManageTagsDialog}
											onTagClick={handleTagFilter}
											{openSection}
											{copyAgentId}
										/>
									{/each}
								{/if}
							</TableBody>
						</Table>
					</div>
				</ScrollArea>
			{:else}
				<div class="space-y-3">
					{#if $clientsTable.paginatedAgents.length === 0}
						<div
							class="rounded-lg border border-border/60 bg-background/80 p-6 text-center text-sm text-muted-foreground"
						>
							{#if $clientsTable.agents.length === 0}
								No agents connected yet.
								<Button type="button" class="mt-3" onclick={() => (deployDialogOpen = true)}>
									View deployment guide
								</Button>
							{:else}
								No agents match your current filters.
							{/if}
						</div>
					{:else}
						{#each $clientsTable.paginatedAgents as agent (agent.id)}
							<ClientsTableRow
								layout="card"
								{agent}
								{formatDate}
								{formatPing}
								{getAgentTags}
								{getAgentLocation}
								ipLocations={$ipLocationStore}
								openManageTags={openManageTagsDialog}
								onTagClick={handleTagFilter}
								{openSection}
								{copyAgentId}
							/>
						{/each}
					{/if}
				</div>
			{/if}
		</TooltipProvider>

		<div class="px-1 text-sm text-muted-foreground md:px-4">
			{#if $clientsTable.filteredAgents.length === 0}
				No agents to display.
			{:else}
				Showing {$clientsTable.pageRange.start}–{$clientsTable.pageRange.end} of {$clientsTable
					.filteredAgents.length}
				{$clientsTable.filteredAgents.length === 1 ? ' agent' : ' agents'}.
			{/if}
		</div>

		{#if $clientsTable.filteredAgents.length > 0 && $clientsTable.totalPages > 1}
			<nav class="flex justify-center">
				<ul class="flex flex-row items-center gap-1">
					<li>
						<Button
							type="button"
							variant="ghost"
							class="flex h-9 items-center gap-1 px-2.5 sm:pl-2.5"
							onclick={() => clientsTable.previousPage()}
							disabled={$clientsTable.currentPage <= 1}
						>
							<ChevronLeft class="size-4" />
							<span class="sr-only sm:not-sr-only">Previous</span>
						</Button>
					</li>
					{#each $clientsTable.paginationItems as item, index (typeof item === 'number' ? `page-${item}` : `ellipsis-${index}`)}
						<li>
							{#if item === 'ellipsis'}
								<span class="flex h-9 w-9 items-center justify-center text-sm text-muted-foreground"
									>…</span
								>
							{:else}
								<Button
									type="button"
									variant={item === $clientsTable.currentPage ? 'outline' : 'ghost'}
									class="h-9 w-9 px-0"
									onclick={() => clientsTable.goToPage(item)}
								>
									{item}
								</Button>
							{/if}
						</li>
					{/each}
					<li>
						<Button
							type="button"
							variant="ghost"
							class="flex h-9 items-center gap-1 px-2.5 sm:pl-2.5"
							onclick={() => clientsTable.nextPage()}
							disabled={$clientsTable.currentPage >= $clientsTable.totalPages}
						>
							<span class="sr-only sm:not-sr-only">Next</span>
							<ChevronRight class="size-4" />
						</Button>
					</li>
				</ul>
			</nav>
		{/if}
	</div>

	{#if toolDialog && toolDialogAgent && toolDialogClient}
		{#key `${toolDialog.agentId}-${toolDialog.toolId}`}
			<ClientToolDialog
				client={toolDialogClient}
				agent={toolDialogAgent}
				toolId={toolDialog.toolId}
				on:close={() => (toolDialog = null)}
			/>
		{/key}
	{/if}

	<DeployAgentDialog open={deployDialogOpen} onClose={() => (deployDialogOpen = false)} />

	<ManageTagsDialog
		open={Boolean(tagsDialogAgentId && tagsAgent)}
		agent={tagsAgent}
		availableTags={$clientsTable.availableTags}
		pending={tagsDialogPending}
		error={tagsDialogError}
		on:close={closeManageTagsDialog}
		on:submit={handleTagsSubmit}
	/>
</section>
