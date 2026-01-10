<script lang="ts">
	import { SvelteSet, SvelteMap } from 'svelte/reactivity';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import {
		Select,
		SelectContent,
		SelectItem,
		SelectTrigger
	} from '$lib/components/ui/select/index.js';
	import {
		Card,
		CardContent,
		CardDescription,
		CardFooter,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { getClientTool } from '$lib/data/client-tools';
	import type { Client } from '$lib/data/clients';
	import { fetchTriggerMonitorStatus, updateTriggerMonitorConfig } from '$lib/data/trigger-monitor';
	import type {
		TriggerMonitorEvent,
		TriggerMonitorMetric,
		TriggerMonitorWatchlist,
		TriggerMonitorWatchlistEntry
	} from '$lib/types/trigger-monitor';
	import { MAX_TRIGGER_MONITOR_WATCHLIST_ENTRIES } from '$lib/types/trigger-monitor';
	import type { ProcessListResponse } from '$lib/types/task-manager';
	import {
		downloadCatalogueResponseSchema,
		type DownloadCatalogueEntry
	} from '$lib/types/downloads';
	import { Plus, Trash2 } from '@lucide/svelte';
	import { appendWorkspaceLog, createWorkspaceLogEntry } from '$lib/workspace/utils';
	import type { WorkspaceLogEntry } from '$lib/workspace/types';

	const { client } = $props<{ client: Client }>();

	const tool = getClientTool('trigger-monitor');
	void tool;

	let feed = $state<'live' | 'batch'>('live');
	let refreshSeconds = $state(5);
	let includeScreenshots = $state(false);
	let includeCommands = $state(true);
	let watchlist = $state<TriggerMonitorWatchlist>([]);
	let metrics = $state<TriggerMonitorMetric[]>([]);
	let events = $state<TriggerMonitorEvent[]>([]);
	let generatedAt = $state<string | null>(null);
	let log = $state<WorkspaceLogEntry[]>([]);
	let loading = $state(true);
	let loadError = $state<string | null>(null);
	let saving = $state(false);
	let watchlistDialogOpen = $state(false);
	let watchlistDraft = $state<TriggerMonitorWatchlist>([]);
	let watchlistSuggestions = $state<WatchlistSuggestion[]>([]);
	let watchlistSuggestionsLoading = $state(false);
	let watchlistSuggestionsLoaded = $state(false);
	let watchlistSuggestionsError = $state<string | null>(null);
	let watchlistFilter = $state('');
	let draftNotice = $state<string | null>(null);
	let urlInput = $state('');
	let urlLabelInput = $state('');
	let urlAlertOnOpen = $state(true);
	let urlAlertOnClose = $state(false);

	type WatchlistSuggestionSource = 'download' | 'process';

	type WatchlistSuggestion = {
		id: string;
		displayName: string;
		detail?: string;
		source: WatchlistSuggestionSource;
	};

	const filteredSuggestions = $derived(() => {
		if (!watchlistFilter.trim()) {
			return watchlistSuggestions;
		}
		const query = watchlistFilter.trim().toLowerCase();
		return watchlistSuggestions.filter((item) => {
			return (
				item.displayName.toLowerCase().includes(query) ||
				item.id.toLowerCase().includes(query) ||
				(item.detail ? item.detail.toLowerCase().includes(query) : false)
			);
		});
	}) as unknown as WatchlistSuggestion[];

	const recentEvents = $derived(() => events.slice(0, 12)) as unknown as TriggerMonitorEvent[];

	const seenEventIds = new SvelteSet<string>();

	function applyEventFeed(incoming: TriggerMonitorEvent[]) {
		const snapshots = incoming.map((entry) => ({ ...entry }));
		events = snapshots;
		for (const entry of incoming) {
			if (seenEventIds.has(entry.id)) {
				continue;
			}
			seenEventIds.add(entry.id);
			const action = entry.event === 'open' ? 'opened' : 'closed';
			const origin = entry.entryKind === 'app' ? 'Application' : 'URL';
			const details = [entry.detail?.trim(), `${origin} ${action} · observed ${entry.observedAt}`]
				.filter((value): value is string => Boolean(value && value.length > 0))
				.join(' · ');
			log = appendWorkspaceLog(
				log,
				createWorkspaceLogEntry(`${entry.displayName} ${action}`, details || undefined, 'complete')
			);
		}
	}

	function describePlan(): string {
		return `${feed} feed · refresh ${refreshSeconds}s · screenshots ${includeScreenshots ? 'on' : 'off'} · commands${
			includeCommands ? 'included' : 'excluded'
		}`;
	}

	function cloneWatchlist(entries: TriggerMonitorWatchlist): TriggerMonitorWatchlist {
		return entries.map((entry: TriggerMonitorWatchlistEntry) => ({ ...entry }));
	}

	function watchlistEntryKey(entry: TriggerMonitorWatchlistEntry): string {
		return `${entry.kind}:${entry.id.trim().toLowerCase()}`;
	}

	async function refreshStatus(signal?: AbortSignal) {
		loadError = null;
		loading = true;
		try {
			const status = await fetchTriggerMonitorStatus(client.id, { signal });
			feed = status.config.feed;
			refreshSeconds = status.config.refreshSeconds;
			includeScreenshots = status.config.includeScreenshots;
			includeCommands = status.config.includeCommands;
			watchlist = status.config.watchlist;
			metrics = status.metrics;
			applyEventFeed(status.events ?? []);
			generatedAt = status.generatedAt;
			if (!watchlistDialogOpen) {
				watchlistDraft = cloneWatchlist(watchlist);
			}
		} catch (err) {
			loadError = (err as Error).message ?? 'Failed to load trigger monitor status';
		} finally {
			loading = false;
		}
	}

	function ensureWatchlistCapacity(): boolean {
		if (watchlistDraft.length < MAX_TRIGGER_MONITOR_WATCHLIST_ENTRIES) {
			return true;
		}
		draftNotice = `Watchlist cannot exceed ${MAX_TRIGGER_MONITOR_WATCHLIST_ENTRIES} entries.`;
		return false;
	}

	function addDraftEntry(entry: TriggerMonitorWatchlistEntry) {
		const key = watchlistEntryKey(entry);
		const existingIndex = watchlistDraft.findIndex((item: TriggerMonitorWatchlistEntry) => {
			return watchlistEntryKey(item) === key;
		});
		if (existingIndex !== -1) {
			watchlistDraft = watchlistDraft.map((item: TriggerMonitorWatchlistEntry, index: number) =>
				index === existingIndex ? { ...item, ...entry } : item
			);
			draftNotice = `${entry.displayName} updated.`;
			return;
		}
		if (!ensureWatchlistCapacity()) {
			return;
		}
		watchlistDraft = [...watchlistDraft, { ...entry }];
		draftNotice = `${entry.displayName} added to watchlist.`;
	}

	function removeDraftEntry(index: number) {
		watchlistDraft = watchlistDraft.filter((_, current: number) => current !== index);
		draftNotice = null;
	}

	function updateDraftEntry(index: number, patch: Partial<TriggerMonitorWatchlistEntry>) {
		watchlistDraft = watchlistDraft.map((entry: TriggerMonitorWatchlistEntry, current: number) =>
			current === index ? { ...entry, ...patch } : entry
		);
	}

	function removeWatchlistEntry(index: number) {
		watchlist = watchlist.filter((_, current: number) => current !== index);
	}

	function updateWatchlistEntry(index: number, patch: Partial<TriggerMonitorWatchlistEntry>) {
		watchlist = watchlist.map((entry: TriggerMonitorWatchlistEntry, current: number) =>
			current === index ? { ...entry, ...patch } : entry
		);
	}

	function interceptToggleEvent(event: MouseEvent | KeyboardEvent): boolean {
		if (event instanceof MouseEvent) {
			if (event.button !== 0) {
				return false;
			}
		} else if (event instanceof KeyboardEvent) {
			if (event.key !== ' ' && event.key !== 'Enter') {
				return false;
			}
		}

		event.preventDefault();
		event.stopImmediatePropagation();
		event.stopPropagation();
		return true;
	}

	function handleWatchlistToggle(
		event: MouseEvent | KeyboardEvent,
		index: number,
		field: 'alertOnOpen' | 'alertOnClose'
	) {
		if (!interceptToggleEvent(event)) {
			return;
		}
		const entry = watchlist[index];
		if (!entry) {
			return;
		}
		updateWatchlistEntry(index, {
			[field]: !entry[field]
		} as Partial<TriggerMonitorWatchlistEntry>);
	}

	function handleDraftToggle(
		event: MouseEvent | KeyboardEvent,
		index: number,
		field: 'alertOnOpen' | 'alertOnClose'
	) {
		if (!interceptToggleEvent(event)) {
			return;
		}
		const entry = watchlistDraft[index];
		if (!entry) {
			return;
		}
		updateDraftEntry(index, {
			[field]: !entry[field]
		} as Partial<TriggerMonitorWatchlistEntry>);
	}

	function openWatchlistManager() {
		watchlistDraft = cloneWatchlist(watchlist);
		draftNotice = null;
		urlInput = '';
		urlLabelInput = '';
		urlAlertOnOpen = true;
		urlAlertOnClose = false;
		watchlistFilter = '';
		watchlistDialogOpen = true;
		if (!watchlistSuggestionsLoaded && !watchlistSuggestionsLoading) {
			void loadWatchlistSuggestions();
		}
	}

	function applyWatchlistChanges() {
		watchlist = cloneWatchlist(watchlistDraft);
		watchlistDialogOpen = false;
	}

	function toDownloadSuggestion(entry: DownloadCatalogueEntry): WatchlistSuggestion {
		const detailSegments = [entry.version?.trim(), entry.description?.trim()]
			.map((value) => (value && value.length > 0 ? value : null))
			.filter((value): value is string => Boolean(value));

		return {
			id: entry.id.trim(),
			displayName: entry.displayName.trim(),
			detail: detailSegments.join(' · ') || undefined,
			source: 'download'
		} satisfies WatchlistSuggestion;
	}

	async function readResponseError(response: Response): Promise<string> {
		try {
			const contentType = response.headers.get('content-type') ?? '';
			if (contentType.includes('application/json')) {
				const payload = (await response.json()) as { message?: string; error?: string };
				return payload?.message || payload?.error || response.statusText || 'Request failed';
			}
			const detail = await response.text();
			return detail || response.statusText || 'Request failed';
		} catch {
			return response.statusText || 'Request failed';
		}
	}

	async function loadWatchlistSuggestions(options: { force?: boolean } = {}) {
		const force = Boolean(options.force);
		if (watchlistSuggestionsLoading && !force) {
			return;
		}
		if (!force && watchlistSuggestionsLoaded) {
			return;
		}

		if (force) {
			watchlistSuggestionsLoaded = false;
		}

		watchlistSuggestionsLoading = true;
		watchlistSuggestionsError = null;
		draftNotice = null;

		const issues: string[] = [];

		async function fetchDownloadSuggestions(): Promise<WatchlistSuggestion[]> {
			try {
				const response = await fetch(`/api/agents/${client.id}/downloads`);
				if (!response.ok) {
					throw new Error(await readResponseError(response));
				}
				const raw = await response.json();
				const parsed = downloadCatalogueResponseSchema.safeParse(raw);
				if (!parsed.success) {
					throw new Error('Unexpected downloads response shape');
				}
				const entries = parsed.data.downloads;
				if (entries.length === 0) {
					issues.push('No downloads catalogue entries are available for this agent.');
					return [];
				}
				return entries.map((entry) => toDownloadSuggestion(entry));
			} catch (err) {
				issues.push(
					`Downloads catalogue unavailable: ${(err as Error).message || 'request failed'}`
				);
				return [];
			}
		}

		async function fetchProcessSuggestions(): Promise<WatchlistSuggestion[]> {
			try {
				const response = await fetch(`/api/agents/${client.id}/task-manager/processes`);
				if (!response.ok) {
					throw new Error(await readResponseError(response));
				}
				const payload = (await response.json()) as ProcessListResponse;
				const suggestions: WatchlistSuggestion[] = [];
				for (const process of payload.processes ?? []) {
					if (!process || typeof process !== 'object') {
						continue;
					}
					const id = (process.command || process.name || '').trim();
					const displayName = (process.name || process.command || '').trim();
					if (!id || !displayName) {
						continue;
					}
					const detailSegments = [
						process.command && process.command !== displayName ? process.command : null,
						typeof process.pid === 'number' ? `PID ${process.pid}` : null
					].filter(Boolean);
					suggestions.push({
						id,
						displayName,
						detail: detailSegments.join(' · ') || undefined,
						source: 'process'
					});
				}
				return suggestions;
			} catch (err) {
				issues.push(
					`Task manager process list unavailable: ${(err as Error).message || 'request failed'}`
				);
				return [];
			}
		}

		try {
			const [downloads, processes] = await Promise.all([
				fetchDownloadSuggestions(),
				fetchProcessSuggestions()
			]);

			const combined = [...downloads, ...processes];
			const dedupedMap = new SvelteMap<string, WatchlistSuggestion>();
			for (const suggestion of combined) {
				const key = `${suggestion.source}:${suggestion.id.toLowerCase()}`;
				if (!dedupedMap.has(key)) {
					dedupedMap.set(key, suggestion);
				}
			}
			watchlistSuggestions = Array.from(dedupedMap.values()).sort((a, b) =>
				a.displayName.localeCompare(b.displayName)
			);
			watchlistSuggestionsError = issues.length > 0 ? issues.join(' ') : null;
		} finally {
			watchlistSuggestionsLoading = false;
			watchlistSuggestionsLoaded = true;
		}
	}

	function handleAddSuggestion(entry: WatchlistSuggestion) {
		addDraftEntry({
			kind: 'app',
			id: entry.id,
			displayName: entry.displayName,
			alertOnOpen: true,
			alertOnClose: false
		});
	}

	function addUrlEntry() {
		const identifier = urlInput.trim();
		if (!identifier) {
			draftNotice = 'Website URL is required.';
			return;
		}
		const displayName = urlLabelInput.trim() || identifier;
		addDraftEntry({
			kind: 'url',
			id: identifier,
			displayName,
			alertOnOpen: urlAlertOnOpen,
			alertOnClose: urlAlertOnClose
		});
		urlInput = '';
		urlLabelInput = '';
		urlAlertOnOpen = true;
		urlAlertOnClose = false;
	}

	async function queue(status: WorkspaceLogEntry['status']) {
		if (status === 'draft') {
			log = appendWorkspaceLog(
				log,
				createWorkspaceLogEntry('Trigger monitor staged', describePlan(), status)
			);
			return;
		}

		saving = true;
		loadError = null;
		const detail = describePlan();
		try {
			const updated = await updateTriggerMonitorConfig(client.id, {
				feed,
				refreshSeconds,
				includeScreenshots,
				includeCommands,
				watchlist
			});
			feed = updated.config.feed;
			refreshSeconds = updated.config.refreshSeconds;
			includeScreenshots = updated.config.includeScreenshots;
			includeCommands = updated.config.includeCommands;
			watchlist = updated.config.watchlist;
			metrics = updated.metrics;
			applyEventFeed(updated.events ?? []);
			generatedAt = updated.generatedAt;
			log = appendWorkspaceLog(
				log,
				createWorkspaceLogEntry('Trigger monitor updated', detail, 'complete')
			);
		} catch (err) {
			const message = (err as Error).message ?? 'Failed to update trigger monitor';
			loadError = message;
			log = appendWorkspaceLog(
				log,
				createWorkspaceLogEntry('Trigger monitor update failed', message, 'failed')
			);
		} finally {
			saving = false;
		}
	}

	onMount(() => {
		const controller = new AbortController();
		void refreshStatus(controller.signal);
		return () => controller.abort();
	});
</script>

<div class="space-y-6">
	{#if loadError}
		<p
			class="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
		>
			{loadError}
		</p>
	{/if}

	<Card>
		<CardHeader class="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
			<div>
				<CardTitle class="text-base">Feed configuration</CardTitle>
				<CardDescription>
					Adjust how frequently telemetry is collected and which channels are enabled.
				</CardDescription>
			</div>
			<Button
				type="button"
				variant="outline"
				size="sm"
				onclick={() => refreshStatus()}
				disabled={loading || saving}
			>
				Refresh
			</Button>
		</CardHeader>
		<CardContent class="space-y-6">
			<div class="grid gap-4 md:grid-cols-3">
				<div class="grid gap-2">
					<Label for="report-feed">Feed type</Label>
					<Select
						type="single"
						value={feed}
						onValueChange={(value) => (feed = value as typeof feed)}
						disabled={saving}
					>
						<SelectTrigger id="report-feed" class="w-full">
							<span class="capitalize">{feed}</span>
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="live">Live</SelectItem>
							<SelectItem value="batch">Batch</SelectItem>
						</SelectContent>
					</Select>
				</div>
				<div class="grid gap-2">
					<Label for="report-refresh">Refresh (seconds)</Label>
					<Input
						id="report-refresh"
						type="number"
						min={feed === 'live' ? 2 : 30}
						step={feed === 'live' ? 1 : 30}
						bind:value={refreshSeconds}
						disabled={saving}
					/>
				</div>
				<label
					class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
				>
					<div>
						<p class="text-sm font-medium text-foreground">Include command stream</p>
						<p class="text-xs text-muted-foreground">Show queued and completed commands</p>
					</div>
					<Switch bind:checked={includeCommands} disabled={saving} />
				</label>
			</div>
			<label
				class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3 md:w-1/2"
			>
				<div>
					<p class="text-sm font-medium text-foreground">Embed screenshots</p>
					<p class="text-xs text-muted-foreground">Attach periodic mini screenshots to the feed</p>
				</div>
				<Switch bind:checked={includeScreenshots} disabled={saving} />
			</label>
		</CardContent>
		<CardFooter class="flex flex-wrap gap-3">
			<Button type="button" variant="outline" onclick={() => queue('draft')} disabled={saving}
				>Save draft</Button
			>
			<Button type="button" onclick={() => queue('queued')} disabled={saving}
				>{saving ? 'Updating…' : 'Queue workspace'}</Button
			>
		</CardFooter>
	</Card>

	<Card>
		<CardHeader class="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
			<div>
				<CardTitle class="text-base">Watchlist</CardTitle>
				<CardDescription>
					Configure applications and websites to monitor for trigger activity.
				</CardDescription>
			</div>
			<Button type="button" variant="outline" size="sm" onclick={openWatchlistManager}>
				<Plus class="mr-2 h-4 w-4" /> Manage watchlist
			</Button>
		</CardHeader>
		<CardContent class="space-y-4">
			{#if watchlist.length === 0}
				<p class="rounded-lg border border-border/60 bg-muted/20 p-4 text-sm text-muted-foreground">
					No watchlist entries configured yet. Use <span class="font-medium">Manage watchlist</span>
					to add installed applications or websites.
				</p>
			{:else}
				<div class="overflow-x-auto">
					<table class="w-full min-w-[640px] table-fixed border-collapse text-sm">
						<thead class="text-left text-xs text-muted-foreground uppercase">
							<tr class="border-b border-border/60">
								<th class="px-3 py-2 font-medium">Type</th>
								<th class="px-3 py-2 font-medium">Display name</th>
								<th class="px-3 py-2 font-medium">Identifier</th>
								<th class="px-3 py-2 text-center font-medium">Alert on open</th>
								<th class="px-3 py-2 text-center font-medium">Alert on close</th>
								<th class="px-3 py-2"></th>
							</tr>
						</thead>
						<tbody>
							{#each watchlist as entry, index (watchlistEntryKey(entry))}
								<tr class="border-b border-border/40 last:border-b-0">
									<td class="px-3 py-3 align-middle">
										<Badge
											variant={entry.kind === 'app' ? 'secondary' : 'outline'}
											class="capitalize"
										>
											{entry.kind}
										</Badge>
									</td>
									<td class="px-3 py-3 align-middle font-medium text-foreground">
										{entry.displayName}
									</td>
									<td class="px-3 py-3 align-middle">
										<code class="text-xs text-muted-foreground">{entry.id}</code>
									</td>
									<td class="px-3 py-3 text-center align-middle">
										<Switch
											checked={entry.alertOnOpen}
											onclick={(event) => handleWatchlistToggle(event, index, 'alertOnOpen')}
											onkeydown={(event) => handleWatchlistToggle(event, index, 'alertOnOpen')}
											disabled={saving}
										/>
									</td>
									<td class="px-3 py-3 text-center align-middle">
										<Switch
											checked={entry.alertOnClose}
											onclick={(event) => handleWatchlistToggle(event, index, 'alertOnClose')}
											onkeydown={(event) => handleWatchlistToggle(event, index, 'alertOnClose')}
											disabled={saving}
										/>
									</td>
									<td class="px-3 py-3 text-right align-middle">
										<Button
											type="button"
											variant="ghost"
											size="icon"
											class="text-muted-foreground hover:text-destructive"
											onclick={() => removeWatchlistEntry(index)}
											disabled={saving}
										>
											<Trash2 class="h-4 w-4" />
										</Button>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</CardContent>
	</Card>

	<Card class="border-dashed">
		<CardHeader>
			<CardTitle class="text-base">Telemetry metrics</CardTitle>
			<CardDescription>
				Latest metrics reported by the agent.
				{#if generatedAt}
					<span class="ml-2 text-xs text-muted-foreground">Generated {generatedAt}</span>
				{/if}
			</CardDescription>
		</CardHeader>
		<CardContent class="grid gap-4 md:grid-cols-3">
			{#if loading}
				<p
					class="col-span-full rounded-lg border border-border/40 bg-muted/30 p-3 text-muted-foreground"
				>
					Loading telemetry…
				</p>
			{:else if metrics.length === 0}
				<p
					class="col-span-full rounded-lg border border-border/60 bg-muted/30 p-3 text-muted-foreground"
				>
					No telemetry has been reported yet.
				</p>
			{:else}
				{#each metrics as metric (metric.id)}
					<div class="rounded-lg border border-border/60 bg-muted/30 p-4">
						<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
							{metric.label}
						</p>
						<p class="mt-2 text-lg font-semibold text-foreground">{metric.value}</p>
					</div>
				{/each}
			{/if}
		</CardContent>
	</Card>

	{#if recentEvents.length > 0}
		<Card>
			<CardHeader>
				<CardTitle class="text-base">Recent alerts</CardTitle>
				<CardDescription>Latest watchlist activity reported by the agent.</CardDescription>
			</CardHeader>
			<CardContent>
				<ul class="space-y-3">
					{#each recentEvents as event (event.id)}
						<li class="rounded-lg border border-border/60 bg-muted/20 p-3 text-sm">
							<div class="flex items-center justify-between gap-3">
								<p class="font-medium text-foreground">{event.displayName}</p>
								<Badge variant={event.event === 'open' ? 'secondary' : 'outline'} class="uppercase">
									{event.event}
								</Badge>
							</div>
							<p class="mt-1 text-xs text-muted-foreground">
								{event.entryKind === 'app' ? 'Application' : 'URL'} · Observed {event.observedAt}
							</p>
							{#if event.detail}
								<p class="text-xs text-muted-foreground">{event.detail}</p>
							{/if}
						</li>
					{/each}
				</ul>
			</CardContent>
		</Card>
	{/if}

	{#if log.length > 0}
		<Card>
			<CardHeader>
				<CardTitle class="text-base">Activity</CardTitle>
				<CardDescription>Recent trigger monitor actions.</CardDescription>
			</CardHeader>
			<CardContent class="space-y-2 text-sm">
				<ul class="space-y-2">
					{#each log as entry (entry.id)}
						<li class="rounded-lg border border-border/60 bg-muted/30 p-3">
							<p class="font-medium text-foreground">{entry.action}</p>
							<p class="text-xs text-muted-foreground">
								Status: {entry.status} · {entry.timestamp}
							</p>
							{#if entry.detail}
								<p class="text-xs text-muted-foreground">{entry.detail}</p>
							{/if}
						</li>
					{/each}
				</ul>
			</CardContent>
		</Card>
	{/if}

	<Dialog.Root bind:open={watchlistDialogOpen}>
		<Dialog.Content
			class="max-h-[80vh] w-full max-w-3xl overflow-hidden rounded-xl border border-border/70 bg-background p-0 shadow-xl"
		>
			<Dialog.Header class="space-y-2 border-b border-border/60 px-6 py-4">
				<Dialog.Title class="text-base font-semibold">Configure watchlist</Dialog.Title>
				<Dialog.Description class="text-sm text-muted-foreground">
					Add installed applications or define website URLs to monitor. Alerts trigger according to
					the switches configured for each entry.
				</Dialog.Description>
			</Dialog.Header>
			<div class="grid gap-6 overflow-y-auto px-6 py-5 md:grid-cols-[1.1fr_1fr]">
				<section class="space-y-4">
					<div class="space-y-2">
						<Label for="watchlist-search">Installed applications</Label>
						<Input
							id="watchlist-search"
							placeholder="Search downloads or running processes"
							bind:value={watchlistFilter}
							disabled={watchlistSuggestionsLoading && watchlistSuggestions.length === 0}
						/>
						<p class="text-xs text-muted-foreground">
							Suggestions combine the downloads catalogue (when available) and the current task
							manager process list.
						</p>
						{#if watchlistSuggestionsError}
							<p
								class="rounded-md border border-border/70 bg-muted/20 p-2 text-xs text-muted-foreground"
							>
								{watchlistSuggestionsError}
							</p>
						{/if}
					</div>
					<div class="space-y-2">
						<div class="flex items-center justify-between text-xs text-muted-foreground">
							<span>{filteredSuggestions.length} suggestions</span>
							<button
								type="button"
								class="underline-offset-4 hover:underline"
								onclick={() => void loadWatchlistSuggestions({ force: true })}
							>
								Refresh
							</button>
						</div>
						<div class="max-h-64 overflow-y-auto rounded-lg border border-border/60">
							{#if watchlistSuggestionsLoading && filteredSuggestions.length === 0}
								<p class="px-4 py-6 text-sm text-muted-foreground">Loading suggestions…</p>
							{:else if filteredSuggestions.length === 0}
								<p class="px-4 py-6 text-sm text-muted-foreground">
									No suggestions available. Try refreshing or adjust the search query.
								</p>
							{:else}
								<ul class="divide-y divide-border/60">
									{#each filteredSuggestions as suggestion (suggestion.source + ':' + suggestion.id)}
										{@const normalizedId = suggestion.id.trim().toLowerCase()}
										{@const existing = watchlistDraft.some(
											(entry) =>
												entry.kind === 'app' && entry.id.trim().toLowerCase() === normalizedId
										)}
										<li class="flex items-start gap-3 px-4 py-3 text-sm">
											<div class="flex-1 space-y-1">
												<div class="flex flex-wrap items-center gap-2">
													<span class="font-medium text-foreground">{suggestion.displayName}</span>
													<Badge
														variant={suggestion.source === 'download' ? 'secondary' : 'outline'}
													>
														{suggestion.source === 'download' ? 'Download' : 'Process'}
													</Badge>
												</div>
												<p class="text-xs text-muted-foreground">
													<code>{suggestion.id}</code>
													{#if suggestion.detail}
														<span class="ml-2">· {suggestion.detail}</span>
													{/if}
												</p>
											</div>
											<Button
												type="button"
												variant="outline"
												size="sm"
												onclick={() => handleAddSuggestion(suggestion)}
												disabled={existing}
											>
												{existing ? 'Added' : 'Add'}
											</Button>
										</li>
									{/each}
								</ul>
							{/if}
						</div>
					</div>
				</section>
				<section class="space-y-4">
					<div class="space-y-2">
						<Label for="watchlist-url">Website URL</Label>
						<Input
							id="watchlist-url"
							type="url"
							placeholder="https://example.com"
							bind:value={urlInput}
						/>
					</div>
					<div class="space-y-2">
						<Label for="watchlist-url-label">Display name</Label>
						<Input id="watchlist-url-label" placeholder="Example site" bind:value={urlLabelInput} />
					</div>
					<div class="grid gap-3 rounded-lg border border-border/60 bg-muted/20 p-3 text-sm">
						<label class="flex items-center justify-between gap-3">
							<span class="text-foreground">Alert when opened</span>
							<Switch bind:checked={urlAlertOnOpen} />
						</label>
						<label class="flex items-center justify-between gap-3">
							<span class="text-foreground">Alert when closed</span>
							<Switch bind:checked={urlAlertOnClose} />
						</label>
					</div>
					<Button type="button" onclick={addUrlEntry}>
						<Plus class="mr-2 h-4 w-4" /> Add website
					</Button>
					<div class="space-y-3">
						<p class="text-xs text-muted-foreground">
							Active draft ({watchlistDraft.length}/{MAX_TRIGGER_MONITOR_WATCHLIST_ENTRIES})
						</p>
						{#if draftNotice}
							<p
								class="rounded-md border border-border/60 bg-muted/30 p-2 text-xs text-muted-foreground"
							>
								{draftNotice}
							</p>
						{/if}
						{#if watchlistDraft.length === 0}
							<p
								class="rounded-lg border border-border/60 bg-muted/20 p-3 text-sm text-muted-foreground"
							>
								Watchlist draft is empty. Add an application or website to begin.
							</p>
						{:else}
							<ul class="space-y-3">
								{#each watchlistDraft as entry, index (watchlistEntryKey(entry))}
									<li class="rounded-lg border border-border/60 bg-background p-3 shadow-sm">
										<div class="flex items-start justify-between gap-3">
											<div>
												<div class="flex items-center gap-2">
													<span class="font-medium text-foreground">{entry.displayName}</span>
													<Badge
														variant={entry.kind === 'app' ? 'secondary' : 'outline'}
														class="capitalize"
													>
														{entry.kind}
													</Badge>
												</div>
												<p class="mt-1 text-xs text-muted-foreground">
													<code>{entry.id}</code>
												</p>
											</div>
											<Button
												type="button"
												variant="ghost"
												size="icon"
												class="text-muted-foreground hover:text-destructive"
												onclick={() => removeDraftEntry(index)}
											>
												<Trash2 class="h-4 w-4" />
											</Button>
										</div>
										<div
											class="mt-3 grid gap-2 rounded-md border border-border/50 bg-muted/20 p-3 text-xs"
										>
											<label class="flex items-center justify-between gap-3">
												<span class="text-foreground">Alert when opened</span>
												<Switch
													checked={entry.alertOnOpen}
													onclick={(event) => handleDraftToggle(event, index, 'alertOnOpen')}
													onkeydown={(event) => handleDraftToggle(event, index, 'alertOnOpen')}
												/>
											</label>
											<label class="flex items-center justify-between gap-3">
												<span class="text-foreground">Alert when closed</span>
												<Switch
													checked={entry.alertOnClose}
													onclick={(event) => handleDraftToggle(event, index, 'alertOnClose')}
													onkeydown={(event) => handleDraftToggle(event, index, 'alertOnClose')}
												/>
											</label>
										</div>
									</li>
								{/each}
							</ul>
						{/if}
					</div>
				</section>
			</div>
			<Dialog.Footer
				class="flex items-center justify-between gap-3 border-t border-border/60 px-6 py-4"
			>
				<p class="text-xs text-muted-foreground">
					Changes are applied to the workspace immediately after clicking <span class="font-medium"
						>Apply watchlist</span
					>.
				</p>
				<div class="flex items-center gap-3">
					<Dialog.Close>
						{#snippet child({ props })}
							<Button type="button" variant="outline" {...props}>Cancel</Button>
						{/snippet}
					</Dialog.Close>
					<Button type="button" onclick={applyWatchlistChanges}>Apply watchlist</Button>
				</div>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
</div>
