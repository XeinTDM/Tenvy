<script lang="ts">
	import { cn } from '$lib/utils.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Card, CardContent } from '$lib/components/ui/card/index.js';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover/index.js';
	import ClientPresenceMap from './client-presence-map.lazy.svelte';
	import { ChevronDown, Earth } from '@lucide/svelte';
	import { countryCodeToFlag } from '$lib/utils/location';
	import type { DashboardClient, DashboardLogEntry } from '$lib/data/dashboard';

	let {
		clients,
		logs,
		generatedAt,
		selectedCountry = $bindable(null)
	}: {
		clients: DashboardClient[];
		logs: DashboardLogEntry[];
		generatedAt: string;
		selectedCountry: string | null;
	} = $props();

	const timeFormatter = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });
	const relativeFormatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
	const generatedAtDate = new Date(generatedAt);

	let activeView = $state<'logs' | 'map'>('map');

	const severityVariant: Record<
		DashboardLogEntry['severity'],
		'secondary' | 'outline' | 'destructive'
	> = {
		info: 'secondary',
		warning: 'outline',
		critical: 'destructive'
	};

	const severityTone: Record<DashboardLogEntry['severity'], string> = {
		info: 'text-sky-500',
		warning: 'text-amber-500',
		critical: 'text-destructive'
	};

	const filteredLogs = $derived(
		selectedCountry
			? logs.filter((entry: DashboardLogEntry) => entry.countryCode === selectedCountry)
			: logs
	);

	const filteredClients = $derived(
		selectedCountry
			? clients.filter((entry: DashboardClient) => entry.location.countryCode === selectedCountry)
			: clients
	);

	function formatRelative(timestamp: string): string {
		const difference = new Date(timestamp).getTime() - generatedAtDate.getTime();
		const abs = Math.abs(difference);
		if (abs < 60_000) {
			return 'moments ago';
		}
		if (abs < 3_600_000) {
			return relativeFormatter.format(Math.round(difference / 60_000), 'minute');
		}
		if (abs < 86_400_000) {
			return relativeFormatter.format(Math.round(difference / 3_600_000), 'hour');
		}
		return relativeFormatter.format(Math.round(difference / 86_400_000), 'day');
	}

	function formatLogTime(timestamp: string): string {
		return timeFormatter.format(new Date(timestamp));
	}

	function resolveFlag(code: string | null): string {
		return code ? countryCodeToFlag(code) : '🌐';
	}
</script>

<Card class="flex h-[min(26rem,65vh)] flex-col border-border/60 lg:col-span-5 lg:h-128">
	<CardContent class="relative flex min-h-0 flex-1 flex-col gap-4 overflow-hidden">
		<div class="pointer-events-none absolute top-4 right-4 z-10">
			<Popover>
				<PopoverTrigger
					type="button"
					class="pointer-events-auto flex h-9 w-9 items-center justify-center rounded-full border border-border/60 bg-background/95 text-muted-foreground shadow-sm transition hover:text-foreground focus-visible:ring-2 focus-visible:ring-primary/60 focus-visible:outline-none"
					aria-label="Open operations view menu"
				>
					<ChevronDown class="h-4 w-4" />
					<span class="sr-only">Toggle operations view menu</span>
				</PopoverTrigger>
				<PopoverContent align="end" sideOffset={12} class="w-36 space-y-2 p-3">
					<Button
						type="button"
						variant={activeView === 'map' ? 'secondary' : 'ghost'}
						size="sm"
						class="w-full text-xs"
						onclick={() => (activeView = 'map')}
					>
						Map
					</Button>
					<Button
						type="button"
						variant={activeView === 'logs' ? 'secondary' : 'ghost'}
						size="sm"
						class="w-full text-xs"
						onclick={() => (activeView = 'logs')}
					>
						Logs
					</Button>
				</PopoverContent>
			</Popover>
		</div>
		{#if activeView === 'map'}
			<div class="min-h-0 flex-1 overflow-hidden">
				<ClientPresenceMap clients={filteredClients} highlightCountry={selectedCountry} />
			</div>
		{:else}
			<div class="min-h-0 flex-1 space-y-3 overflow-y-auto pr-1">
				{#if filteredLogs.length === 0}
					<div
						class="rounded-lg border border-dashed border-border/60 p-6 text-center text-sm text-muted-foreground"
					>
						{#if logs.length === 0}
							No operations have been logged yet.
						{:else}
							No events matched this country filter.
						{/if}
					</div>
				{/if}
				{#each filteredLogs as entry (entry.id)}
					<div
						class="flex flex-col gap-4 rounded-lg border border-border/60 p-4 md:flex-row md:items-center md:justify-between"
					>
						<div class="flex items-start gap-3">
							<span
								class="flex h-10 w-10 items-center justify-center rounded-md border border-border/60 bg-muted/40"
							>
								<Earth class="h-4 w-4 text-muted-foreground" />
							</span>
							<div class="space-y-1">
								<div class="flex items-center gap-2 text-sm font-semibold">
									<span>{entry.codename}</span>
									<span class="text-xs text-muted-foreground">
										{resolveFlag(entry.countryCode ?? null)}
									</span>
								</div>
								<p
									class="font-mono text-[0.65rem] tracking-[0.08em] text-muted-foreground uppercase"
								>
									{entry.action}
								</p>
								<p class="text-sm text-muted-foreground">{entry.description}</p>
							</div>
						</div>
						<div class="flex flex-col items-start gap-2 md:items-end">
							<div class="flex items-center gap-2 text-xs text-muted-foreground">
								<span>{formatLogTime(entry.timestamp)}</span>
								<span aria-hidden="true">•</span>
								<span>{formatRelative(entry.timestamp)}</span>
							</div>
							<Badge
								variant={severityVariant[entry.severity]}
								class={cn('tracking-wide uppercase', severityTone[entry.severity])}
							>
								{entry.severity}
							</Badge>
						</div>
					</div>
				{/each}
			</div>
		{/if}
	</CardContent>
</Card>
