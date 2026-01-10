<script lang="ts">
	import { SvelteSet } from 'svelte/reactivity';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import {
		Select,
		SelectContent,
		SelectItem,
		SelectTrigger
	} from '$lib/components/ui/select/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import {
		Card,
		CardContent,
		CardDescription,
		CardFooter,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table/index.js';
	import { getClientTool, type DialogToolId } from '$lib/data/client-tools';
	import type { Client } from '$lib/data/clients';
	import { appendWorkspaceLog, createWorkspaceLogEntry } from '$lib/workspace/utils';
	import { notifyToolActivationCommand } from '$lib/utils/agent-commands.js';
	import type { WorkspaceLogEntry } from '$lib/workspace/types';
	import WorkspaceHeroHeader from '$lib/components/workspace/WorkspaceHeroHeader.svelte';
	import type {
		StartupEntry,
		StartupImpact,
		StartupInventoryResponse
	} from '$lib/types/startup-manager';

	type SortKey = 'name' | 'impact' | 'status' | 'publisher' | 'startupTime';
	type SortDirection = 'asc' | 'desc';

	const {
		client,
		toolId = 'system-monitor',
		panel = 'startup'
	} = $props<{ client: Client; toolId?: DialogToolId; panel?: 'startup' }>();

	const tool = $derived(getClientTool(toolId));
	const headerTitle = 'Startup manager';
	const headerSubtitle = 'Audit autoruns and tune persistence across scopes.';

	const dateFormatter = new Intl.DateTimeFormat(undefined, {
		dateStyle: 'medium',
		timeStyle: 'short'
	});

	const durationFormatter = new Intl.NumberFormat(undefined, {
		maximumFractionDigits: 1
	});

	let entries = $state<StartupEntry[]>([]);
	let loading = $state(false);
	let loadError = $state<string | null>(null);
	let creating = $state(false);
	let createError = $state<string | null>(null);
	let toggling = $state(new SvelteSet<string>());
	let removing = $state(new SvelteSet<string>());
	let newName = $state('');
	let newPath = $state('');
	let newPublisher = $state('');
	let newDescription = $state('');
	let newArguments = $state('');
	let newScope = $state<'machine' | 'user'>('machine');
	let newImpact = $state<StartupImpact>('medium');
	let newLocation = $state('HKLM:Software\\Microsoft\\Windows\\CurrentVersion\\Run');
	let log = $state<WorkspaceLogEntry[]>([]);
	let searchQuery = $state('');
	let scopeFilter = $state<'all' | StartupEntry['scope']>('all');
	let impactFilter = $state<'all' | StartupImpact>('all');
	let statusFilter = $state<'all' | 'enabled' | 'disabled'>('all');
	let sortKey = $state<SortKey>('impact');
	let sortDirection = $state<SortDirection>('desc');
	let autoRefresh = $state(true);
	let refreshInterval = $state(20);
	let lastRefreshed = $state<string | null>(null);
	let selectedEntry = $state<StartupEntry | null>(null);

	let refreshTimer: ReturnType<typeof setInterval> | null = null;

	function formatTimestamp(value: string | null | undefined): string {
		if (!value) {
			return '—';
		}
		try {
			return dateFormatter.format(new Date(value));
		} catch {
			return value;
		}
	}

	function formatStartupDuration(ms: number): string {
		if (!Number.isFinite(ms) || ms <= 0) {
			return 'Not measured';
		}
		return `${durationFormatter.format(ms / 1000)} s`;
	}

	function formatScope(scope: StartupEntry['scope']): string {
		switch (scope) {
			case 'machine':
				return 'Machine';
			case 'user':
				return 'User';
			case 'scheduled-task':
				return 'Scheduled task';
			default:
				return scope;
		}
	}

	function formatScopeFilter(scope: StartupEntry['scope'] | 'all'): string {
		if (scope === 'all') {
			return 'All';
		}
		return formatScope(scope);
	}

	function impactBadgeVariant(
		impact: StartupImpact
	): 'default' | 'secondary' | 'destructive' | 'outline' {
		switch (impact) {
			case 'high':
				return 'destructive';
			case 'medium':
				return 'default';
			case 'low':
				return 'secondary';
			case 'not-measured':
			default:
				return 'outline';
		}
	}

	function impactLabel(impact: StartupImpact): string {
		switch (impact) {
			case 'high':
				return 'High impact';
			case 'medium':
				return 'Medium impact';
			case 'low':
				return 'Low impact';
			case 'not-measured':
				return 'Not measured';
			default:
				return impact;
		}
	}

	function recordLog(
		action: string,
		detail: string,
		status: WorkspaceLogEntry['status'] = 'queued',
		metadata?: Record<string, unknown>
	) {
		log = appendWorkspaceLog(log, createWorkspaceLogEntry(action, detail, status));
		notifyToolActivationCommand(client.id, toolId, {
			action: `event:${action}`,
			metadata: {
				detail,
				status,
				panel,
				...metadata
			}
		});
	}

	function syncSelectedEntry() {
		if (!selectedEntry) {
			return;
		}
		const updated = entries.find((entry) => entry.id === selectedEntry?.id) ?? null;
		selectedEntry = updated;
	}

	function requestSort(key: SortKey) {
		if (sortKey === key) {
			sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
			return;
		}
		sortKey = key;
		sortDirection = key === 'name' || key === 'publisher' ? 'asc' : 'desc';
	}

	function selectEntry(entry: StartupEntry) {
		selectedEntry = entry;
	}

	function updateTogglePending(entryId: string, pending: boolean) {
		const next = new Set(toggling);
		if (pending) {
			next.add(entryId);
		} else {
			next.delete(entryId);
		}
		toggling = next;
	}

	function updateRemoving(entryId: string, pending: boolean) {
		const next = new Set(removing);
		if (pending) {
			next.add(entryId);
		} else {
			next.delete(entryId);
		}
		removing = next;
	}

	function resetForm() {
		newName = '';
		newPath = '';
		newPublisher = '';
		newDescription = '';
		newArguments = '';
		newScope = 'machine';
		newImpact = 'medium';
		newLocation = 'HKLM:Software\\Microsoft\\Windows\\CurrentVersion\\Run';
	}

	async function loadEntries(options: { silent?: boolean } = {}): Promise<boolean> {
		if (!options.silent) {
			loading = true;
			loadError = null;
		}
		let success = false;
		try {
			const response = await fetch(`/api/agents/${client.id}/startup`);
			if (!response.ok) {
				const detail = await response.text().catch(() => '');
				throw new Error(detail || `Request failed with status ${response.status}`);
			}
			const payload = (await response.json()) as StartupInventoryResponse;
			entries = payload.entries;
			lastRefreshed = payload.generatedAt;
			loadError = null;
			syncSelectedEntry();
			success = true;
		} catch (err) {
			loadError = (err as Error).message || 'Failed to load startup inventory';
		} finally {
			if (!options.silent) {
				loading = false;
			}
		}
		return success;
	}

	async function refreshInventory(options: { silent?: boolean } = {}) {
		const success = await loadEntries(options);
		if (options.silent) {
			return;
		}
		if (success) {
			recordLog(
				'Startup inventory synchronised',
				`Evaluated ${entries.length} entries`,
				'complete',
				{ total: entries.length }
			);
		} else if (loadError) {
			recordLog('Startup inventory refresh failed', loadError, 'complete');
		}
	}

	async function toggleEntry(entry: StartupEntry, enabled: boolean) {
		if (toggling.has(entry.id)) {
			return;
		}
		const original = { ...entry };
		updateTogglePending(entry.id, true);
		entries = entries.map((item) =>
			item.id === entry.id ? { ...item, enabled, lastEvaluatedAt: new Date().toISOString() } : item
		);
		syncSelectedEntry();
		recordLog(
			'Startup entry toggle requested',
			`${entry.name} → ${enabled ? 'enabled' : 'disabled'}`,
			'queued',
			{
				entryId: entry.id,
				enabled,
				name: entry.name,
				scope: entry.scope
			}
		);
		try {
			const response = await fetch(
				`/api/agents/${client.id}/startup/${encodeURIComponent(entry.id)}`,
				{
					method: 'PATCH',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ enabled })
				}
			);
			if (!response.ok) {
				const detail = await response.text().catch(() => '');
				throw new Error(detail || `Request failed with status ${response.status}`);
			}
			const updated = (await response.json()) as StartupEntry;
			entries = entries.map((item) => (item.id === updated.id ? updated : item));
			syncSelectedEntry();
			recordLog(
				'Startup entry toggled',
				`${updated.name} → ${updated.enabled ? 'enabled' : 'disabled'}`,
				'complete',
				{
					entryId: updated.id,
					enabled: updated.enabled,
					name: updated.name,
					scope: updated.scope
				}
			);
		} catch (err) {
			entries = entries.map((item) => (item.id === original.id ? original : item));
			syncSelectedEntry();
			recordLog(
				'Startup entry toggle failed',
				`${original.name}: ${(err as Error).message || 'unexpected error'}`,
				'complete',
				{
					entryId: original.id,
					enabled: original.enabled,
					name: original.name,
					scope: original.scope
				}
			);
		} finally {
			updateTogglePending(entry.id, false);
		}
	}

	async function addEntry(mode: 'draft' | 'queued') {
		const trimmedName = newName.trim();
		const trimmedPath = newPath.trim();
		if (!trimmedName || !trimmedPath) {
			createError = 'Name and path are required';
			return;
		}
		createError = null;
		creating = true;
		const enabled = mode !== 'draft';
		recordLog('Startup entry creation requested', `${trimmedName} (${newScope})`, 'queued', {
			name: trimmedName,
			path: trimmedPath,
			scope: newScope,
			location: newLocation,
			enabled
		});
		try {
			const response = await fetch(`/api/agents/${client.id}/startup`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					name: trimmedName,
					path: trimmedPath,
					scope: newScope,
					source: 'registry',
					location: newLocation.trim() || 'Custom definition',
					publisher: newPublisher.trim() || undefined,
					description: newDescription.trim() || undefined,
					arguments: newArguments.trim() || undefined,
					enabled,
					impact: newImpact
				})
			});
			if (!response.ok) {
				const detail = await response.text().catch(() => '');
				throw new Error(detail || `Request failed with status ${response.status}`);
			}
			const created = (await response.json()) as StartupEntry;
			entries = [created, ...entries.filter((entry) => entry.id !== created.id)];
			selectedEntry = created;
			lastRefreshed = created.lastEvaluatedAt;
			recordLog(
				'Startup entry creation complete',
				`${created.name} (${created.scope})`,
				'complete',
				{
					entryId: created.id,
					scope: created.scope,
					location: created.location
				}
			);
			resetForm();
		} catch (err) {
			const message = (err as Error).message || 'Failed to create startup entry';
			createError = message;
			recordLog('Startup entry creation failed', `${trimmedName}: ${message}`, 'complete', {
				name: trimmedName,
				scope: newScope,
				location: newLocation
			});
		} finally {
			creating = false;
		}
	}

	async function removeEntry(entry: StartupEntry) {
		if (removing.has(entry.id)) {
			return;
		}
		const previous = entries;
		updateRemoving(entry.id, true);
		entries = entries.filter((item) => item.id !== entry.id);
		if (selectedEntry?.id === entry.id) {
			selectedEntry = null;
		}
		recordLog('Startup entry removal requested', `${entry.name} (${entry.scope})`, 'queued', {
			entryId: entry.id,
			scope: entry.scope
		});
		try {
			const response = await fetch(
				`/api/agents/${client.id}/startup/${encodeURIComponent(entry.id)}`,
				{
					method: 'DELETE'
				}
			);
			if (!response.ok) {
				const detail = await response.text().catch(() => '');
				throw new Error(detail || `Request failed with status ${response.status}`);
			}
			recordLog('Startup entry removal complete', `${entry.name} (${entry.scope})`, 'complete', {
				entryId: entry.id,
				scope: entry.scope
			});
		} catch (err) {
			entries = previous;
			syncSelectedEntry();
			recordLog(
				'Startup entry removal failed',
				`${entry.name}: ${(err as Error).message || 'unexpected error'}`,
				'complete',
				{
					entryId: entry.id,
					scope: entry.scope
				}
			);
		} finally {
			updateRemoving(entry.id, false);
		}
	}

	function ensureRefreshTimer(shouldRefresh: boolean, intervalSeconds: number) {
		if (refreshTimer) {
			clearInterval(refreshTimer);
			refreshTimer = null;
		}
		if (!shouldRefresh) {
			return;
		}
		const interval = Math.max(intervalSeconds, 5) * 1000;
		refreshTimer = setInterval(() => {
			void refreshInventory({ silent: true });
		}, interval);
	}

	$effect(() => {
		const shouldRefresh = autoRefresh;
		const intervalSeconds = refreshInterval;
		ensureRefreshTimer(shouldRefresh, intervalSeconds);
		return () => {
			if (refreshTimer) {
				clearInterval(refreshTimer);
				refreshTimer = null;
			}
		};
	});

	onMount(() => {
		void refreshInventory();
		return () => {
			if (refreshTimer) {
				clearInterval(refreshTimer);
			}
		};
	});

	function impactRank(value: StartupImpact): number {
		switch (value) {
			case 'high':
				return 3;
			case 'medium':
				return 2;
			case 'low':
				return 1;
			default:
				return 0;
		}
	}

	const filteredEntries = $derived(
		(() => {
			const term = searchQuery.trim().toLowerCase();
			const scope = scopeFilter;
			const impact = impactFilter;
			const status = statusFilter;
			const list = entries.filter((entry) => {
				if (scope !== 'all' && entry.scope !== scope) {
					return false;
				}
				if (impact !== 'all' && entry.impact !== impact) {
					return false;
				}
				if (status !== 'all') {
					const enabled = status === 'enabled';
					if (entry.enabled !== enabled) {
						return false;
					}
				}
				if (!term) {
					return true;
				}
				const haystack = [
					entry.name,
					entry.path,
					entry.publisher,
					entry.description,
					entry.location,
					entry.arguments ?? ''
				]
					.join(' ')
					.toLowerCase();
				return haystack.includes(term);
			});
			const direction = sortDirection === 'asc' ? 1 : -1;
			return list.sort((a, b) => {
				switch (sortKey) {
					case 'name':
						return a.name.localeCompare(b.name) * direction;
					case 'publisher':
						return (a.publisher ?? '').localeCompare(b.publisher ?? '') * direction;
					case 'impact':
						return (impactRank(b.impact) - impactRank(a.impact)) * direction;
					case 'status':
						return (Number(b.enabled) - Number(a.enabled)) * direction;
					case 'startupTime':
						return (a.startupTime - b.startupTime) * direction;
					default:
						return 0;
				}
			});
		})()
	);

	const heroMetadata = $derived(
		(() => {
			const enabledCount = entries.filter((entry) => entry.enabled).length;
			const highImpact = entries.filter((entry) => entry.impact === 'high').length;
			return [
				{ label: 'Tracked entries', value: entries.length ? `${entries.length}` : '—' },
				{ label: 'Enabled', value: enabledCount ? `${enabledCount}` : '0' },
				{ label: 'High impact', value: highImpact ? `${highImpact}` : '0' },
				{
					label: 'Last sync',
					value: lastRefreshed ? formatTimestamp(lastRefreshed) : 'Synchronising…'
				}
			];
		})()
	);

	const sortOptions: { label: string; value: SortKey }[] = [
		{ label: 'Impact', value: 'impact' },
		{ label: 'Name', value: 'name' },
		{ label: 'Publisher', value: 'publisher' },
		{ label: 'Status', value: 'status' },
		{ label: 'Startup duration', value: 'startupTime' }
	];
	const impactOptions: { label: string; value: StartupImpact | 'all' }[] = [
		{ label: 'All impact levels', value: 'all' },
		{ label: 'High impact', value: 'high' },
		{ label: 'Medium impact', value: 'medium' },
		{ label: 'Low impact', value: 'low' },
		{ label: 'Not measured', value: 'not-measured' }
	];
</script>

<div class="space-y-6">
	<WorkspaceHeroHeader
		{client}
		{tool}
		title={headerTitle}
		subtitle={headerSubtitle}
		metadata={heroMetadata}
	/>
	<Card>
		<CardHeader>
			<CardTitle class="text-base">Add startup entry</CardTitle>
			<CardDescription>
				Define a new executable to run automatically when the host boots or the operator signs in.
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-4">
			<div class="grid gap-4 md:grid-cols-2">
				<div class="grid gap-2">
					<Label for="startup-name">Name</Label>
					<Input id="startup-name" bind:value={newName} placeholder="TelemetryBridge" />
				</div>
				<div class="grid gap-2">
					<Label for="startup-publisher">Publisher</Label>
					<Input
						id="startup-publisher"
						bind:value={newPublisher}
						placeholder="Internal Operations"
					/>
				</div>
			</div>
			<div class="grid gap-4 md:grid-cols-2">
				<div class="grid gap-2">
					<Label for="startup-path">Executable path</Label>
					<Input
						id="startup-path"
						bind:value={newPath}
						placeholder="C:/Program Files/App/app.exe"
					/>
				</div>
				<div class="grid gap-2">
					<Label for="startup-location">Location</Label>
					<Input
						id="startup-location"
						bind:value={newLocation}
						placeholder="HKLM:Software\\Microsoft\\Windows\\CurrentVersion\\Run"
					/>
				</div>
			</div>
			<div class="grid gap-4 md:grid-cols-3">
				<div class="grid gap-2">
					<Label for="startup-scope">Scope</Label>
					<Select
						type="single"
						value={newScope}
						onValueChange={(value) => (newScope = value as typeof newScope)}
					>
						<SelectTrigger id="startup-scope" class="w-full">
							<span class="truncate capitalize">{newScope}</span>
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="machine">Machine</SelectItem>
							<SelectItem value="user">User</SelectItem>
						</SelectContent>
					</Select>
				</div>
				<div class="grid gap-2">
					<Label for="startup-impact">Impact on startup</Label>
					<Select
						type="single"
						value={newImpact}
						onValueChange={(value) => (newImpact = value as StartupImpact)}
					>
						<SelectTrigger id="startup-impact" class="w-full">
							<span class="truncate">{impactLabel(newImpact)}</span>
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="high">High impact</SelectItem>
							<SelectItem value="medium">Medium impact</SelectItem>
							<SelectItem value="low">Low impact</SelectItem>
							<SelectItem value="not-measured">Not measured</SelectItem>
						</SelectContent>
					</Select>
				</div>
				<div class="grid gap-2">
					<Label for="startup-arguments">Arguments</Label>
					<Input
						id="startup-arguments"
						bind:value={newArguments}
						placeholder="--mode=stealth --delay=30"
					/>
				</div>
			</div>
			<div class="grid gap-2">
				<Label for="startup-description">Description</Label>
				<Textarea
					id="startup-description"
					rows={3}
					bind:value={newDescription}
					placeholder="Describe the purpose of this persistence mechanism."
				/>
			</div>
			{#if createError}
				<p class="text-sm text-destructive">{createError}</p>
			{/if}
		</CardContent>
		<CardFooter class="flex flex-wrap gap-3">
			<Button type="button" variant="outline" disabled={creating} onclick={() => addEntry('draft')}>
				Save draft
			</Button>
			<Button type="button" disabled={creating} onclick={() => addEntry('queued')}>
				{creating ? 'Submitting…' : 'Queue addition'}
			</Button>
		</CardFooter>
	</Card>

	<Card class="border-dashed">
		<CardHeader>
			<CardTitle class="text-base">Startup inventory</CardTitle>
			<CardDescription>
				Search, sort, and monitor every persistence entry. Toggle items to enable or disable them in
				real time.
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-5">
			<div class="grid gap-4 lg:grid-cols-2 xl:grid-cols-4">
				<div class="grid gap-2">
					<Label for="startup-search">Search</Label>
					<Input
						id="startup-search"
						bind:value={searchQuery}
						placeholder="Search name, path, or publisher"
					/>
				</div>
				<div class="grid gap-2">
					<Label for="startup-scope-filter">Scope</Label>
					<Select
						type="single"
						value={scopeFilter}
						onValueChange={(value) => (scopeFilter = value as typeof scopeFilter)}
					>
						<SelectTrigger id="startup-scope-filter" class="w-full">
							<span class="truncate">{formatScopeFilter(scopeFilter)}</span>
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="all">All scopes</SelectItem>
							<SelectItem value="machine">Machine</SelectItem>
							<SelectItem value="user">User</SelectItem>
							<SelectItem value="scheduled-task">Scheduled tasks</SelectItem>
						</SelectContent>
					</Select>
				</div>
				<div class="grid gap-2">
					<Label for="startup-impact-filter">Impact</Label>
					<Select
						type="single"
						value={impactFilter}
						onValueChange={(value) => (impactFilter = value as typeof impactFilter)}
					>
						<SelectTrigger id="startup-impact-filter" class="w-full">
							<span class="truncate"
								>{impactOptions.find((item) => item.value === impactFilter)?.label}</span
							>
						</SelectTrigger>
						<SelectContent>
							{#each impactOptions as option (option.value)}
								<SelectItem value={option.value}>{option.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</div>
				<div class="grid gap-2">
					<Label for="startup-status-filter">Status</Label>
					<Select
						type="single"
						value={statusFilter}
						onValueChange={(value) => (statusFilter = value as typeof statusFilter)}
					>
						<SelectTrigger id="startup-status-filter" class="w-full">
							<span class="truncate capitalize">{statusFilter}</span>
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="all">All items</SelectItem>
							<SelectItem value="enabled">Enabled</SelectItem>
							<SelectItem value="disabled">Disabled</SelectItem>
						</SelectContent>
					</Select>
				</div>
			</div>

			{#if loadError}
				<p class="text-sm text-destructive">{loadError}</p>
			{/if}

			<div class="flex flex-wrap items-center gap-3">
				<div
					class="flex items-center gap-3 rounded-lg border border-border/60 bg-muted/30 px-4 py-2 text-sm"
				>
					<div class="flex items-center gap-2">
						<Switch
							checked={autoRefresh}
							onCheckedChange={(value) => (autoRefresh = value)}
							id="startup-auto-refresh"
						/>
						<Label class="m-0 cursor-pointer text-sm" for="startup-auto-refresh">
							Auto-refresh
						</Label>
					</div>
					<div class="flex items-center gap-2">
						<span class="text-xs text-muted-foreground">Interval</span>
						<Select
							type="single"
							value={String(refreshInterval)}
							onValueChange={(value) => (refreshInterval = Number(value) || refreshInterval)}
						>
							<SelectTrigger class="w-28">
								<span>{refreshInterval}s</span>
							</SelectTrigger>
							<SelectContent>
								<SelectItem value="10">10 seconds</SelectItem>
								<SelectItem value="20">20 seconds</SelectItem>
								<SelectItem value="30">30 seconds</SelectItem>
								<SelectItem value="60">60 seconds</SelectItem>
							</SelectContent>
						</Select>
					</div>
				</div>
				<div class="flex flex-1 flex-wrap items-center justify-end gap-3">
					<Select
						type="single"
						value={sortKey}
						onValueChange={(value) => requestSort(value as SortKey)}
					>
						<SelectTrigger class="w-48">
							<span class="truncate">
								Sort by {sortOptions.find((option) => option.value === sortKey)?.label}
								({sortDirection})
							</span>
						</SelectTrigger>
						<SelectContent>
							{#each sortOptions as option (option.value)}
								<SelectItem value={option.value}>{option.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
					<Button
						type="button"
						variant="outline"
						disabled={loading}
						onclick={() => refreshInventory()}
					>
						{loading ? 'Refreshing…' : 'Refresh now'}
					</Button>
				</div>
			</div>

			<div class="overflow-hidden rounded-lg border border-border/60 bg-muted/20">
				<Table class="min-w-full">
					<TableHeader>
						<TableRow>
							<TableHead class="w-[220px]">Name</TableHead>
							<TableHead>Path</TableHead>
							<TableHead class="w-[120px] text-center">Status</TableHead>
							<TableHead class="w-[150px] text-center">Impact</TableHead>
							<TableHead class="w-[140px] text-center">Startup time</TableHead>
							<TableHead class="w-[160px]">Publisher</TableHead>
							<TableHead class="w-[150px] text-center">Actions</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{#if filteredEntries.length === 0}
							<TableRow>
								<TableCell colspan={7} class="text-center text-sm text-muted-foreground">
									No entries match the current filters.
								</TableCell>
							</TableRow>
						{:else}
							{#each filteredEntries as entry (entry.id)}
								<TableRow class="align-top">
									<TableCell>
										<div class="space-y-1">
											<p class="font-medium text-foreground">{entry.name}</p>
											<p class="text-xs text-muted-foreground">
												Scope · {formatScope(entry.scope)}
											</p>
										</div>
									</TableCell>
									<TableCell>
										<p class="truncate text-sm" title={entry.path}>{entry.path}</p>
										{#if entry.arguments}
											<p class="text-xs text-muted-foreground" title={entry.arguments}>
												Args: {entry.arguments}
											</p>
										{/if}
									</TableCell>
									<TableCell class="text-center">
										<div class="flex items-center justify-center gap-2">
											<Badge variant={entry.enabled ? 'secondary' : 'outline'}>
												{entry.enabled ? 'Enabled' : 'Disabled'}
											</Badge>
											<Switch
												checked={entry.enabled}
												disabled={toggling.has(entry.id)}
												onCheckedChange={(value) => toggleEntry(entry, value)}
												aria-label={`Toggle ${entry.name}`}
											/>
										</div>
									</TableCell>
									<TableCell class="text-center">
										<Badge variant={impactBadgeVariant(entry.impact)}>
											{impactLabel(entry.impact)}
										</Badge>
									</TableCell>
									<TableCell class="text-center text-sm">
										{formatStartupDuration(entry.startupTime)}
									</TableCell>
									<TableCell>
										<p class="truncate text-sm" title={entry.publisher ?? 'Unknown publisher'}>
											{entry.publisher ?? 'Unknown publisher'}
										</p>
										<p class="text-xs text-muted-foreground" title={entry.location}>
											{entry.location}
										</p>
									</TableCell>
									<TableCell>
										<div class="flex items-center justify-center gap-2">
											<Button
												type="button"
												size="sm"
												variant="outline"
												onclick={() => selectEntry(entry)}
											>
												Details
											</Button>
											<Button
												type="button"
												size="sm"
												variant="ghost"
												class="text-destructive hover:text-destructive"
												disabled={removing.has(entry.id)}
												onclick={() => removeEntry(entry)}
											>
												{removing.has(entry.id) ? 'Removing…' : 'Remove'}
											</Button>
										</div>
									</TableCell>
								</TableRow>
							{/each}
						{/if}
					</TableBody>
				</Table>
			</div>
		</CardContent>
		<CardFooter
			class="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground"
		>
			<p>
				Showing {filteredEntries.length} of {entries.length} tracked entries.
			</p>
			<p>Last refreshed {lastRefreshed ? formatTimestamp(lastRefreshed) : '—'}.</p>
		</CardFooter>
	</Card>

	<Card>
		<CardHeader>
			<CardTitle class="text-base">Entry details</CardTitle>
			<CardDescription>
				Drill into the selected program to understand its behaviour, provenance, and recent
				activity.
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-4 text-sm">
			{#if selectedEntry}
				<div class="space-y-3">
					<div class="flex flex-wrap items-center gap-2">
						<h3 class="text-base font-semibold text-foreground">{selectedEntry.name}</h3>
						<Badge variant={selectedEntry.enabled ? 'secondary' : 'outline'}>
							{selectedEntry.enabled ? 'Enabled' : 'Disabled'}
						</Badge>
						<Badge variant={impactBadgeVariant(selectedEntry.impact)}>
							{impactLabel(selectedEntry.impact)}
						</Badge>
					</div>
					<p class="text-muted-foreground">{selectedEntry.description}</p>
					<dl class="grid gap-3 md:grid-cols-2">
						<div class="space-y-1">
							<dt class="text-xs text-muted-foreground uppercase">Executable</dt>
							<dd class="font-medium wrap-break-word text-foreground">{selectedEntry.path}</dd>
						</div>
						<div class="space-y-1">
							<dt class="text-xs text-muted-foreground uppercase">Publisher</dt>
							<dd class="font-medium text-foreground">
								{selectedEntry.publisher ?? 'Unknown publisher'}
							</dd>
						</div>
						<div class="space-y-1">
							<dt class="text-xs text-muted-foreground uppercase">Location</dt>
							<dd class="font-medium wrap-break-word text-foreground">{selectedEntry.location}</dd>
						</div>
						<div class="space-y-1">
							<dt class="text-xs text-muted-foreground uppercase">Scope</dt>
							<dd class="font-medium text-foreground capitalize">{selectedEntry.scope}</dd>
						</div>
						<div class="space-y-1">
							<dt class="text-xs text-muted-foreground uppercase">Startup duration</dt>
							<dd class="font-medium text-foreground">
								{formatStartupDuration(selectedEntry.startupTime)}
							</dd>
						</div>
						<div class="space-y-1">
							<dt class="text-xs text-muted-foreground uppercase">Last evaluated</dt>
							<dd class="font-medium text-foreground">
								{formatTimestamp(selectedEntry.lastEvaluatedAt)}
							</dd>
						</div>
						<div class="space-y-1">
							<dt class="text-xs text-muted-foreground uppercase">Last run</dt>
							<dd class="font-medium text-foreground">
								{formatTimestamp(selectedEntry.lastRunAt)}
							</dd>
						</div>
						{#if selectedEntry.arguments}
							<div class="space-y-1 md:col-span-2">
								<dt class="text-xs text-muted-foreground uppercase">Arguments</dt>
								<dd class="font-medium wrap-break-word text-foreground">
									{selectedEntry.arguments}
								</dd>
							</div>
						{/if}
					</dl>
				</div>
			{:else}
				<p class="text-muted-foreground">
					Select a startup entry to review its metadata and runtime insights.
				</p>
			{/if}
		</CardContent>
	</Card>
</div>
