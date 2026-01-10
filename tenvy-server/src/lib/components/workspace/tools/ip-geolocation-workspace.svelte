<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import {
		Select,
		SelectContent,
		SelectItem,
		SelectTrigger
	} from '$lib/components/ui/select/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import {
		Card,
		CardContent,
		CardDescription,
		CardFooter,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { getClientTool } from '$lib/data/client-tools';
	import type { Client } from '$lib/data/clients';
	import { fetchGeoStatus, lookupGeoMetadata } from '$lib/data/ip-geolocation';
	import type { GeoLookupResult, GeoProvider } from '$lib/types/ip-geolocation';
	import { appendWorkspaceLog, createWorkspaceLogEntry } from '$lib/workspace/utils';
	import type { WorkspaceLogEntry } from '$lib/workspace/types';

	const { client } = $props<{ client: Client }>();

	const tool = getClientTool('ip-geolocation');
	void tool;

	let provider = $state<GeoProvider>('ipinfo');
	let providers = $state<GeoProvider[]>(['ipinfo', 'maxmind', 'db-ip']);
	let includeTimezone = $state(true);
	let includeMap = $state(true);
	let cacheHours = $state(6);
	let ipAddress = $state(client.ip ?? '');
	let result = $state<GeoLookupResult | null>(null);
	let generatedAt = $state<string | null>(null);
	let loading = $state(true);
	let loadError = $state<string | null>(null);
	let saving = $state(false);
	let log = $state<WorkspaceLogEntry[]>([]);

	function describePlan(): string {
		return `${provider} provider · ip ${ipAddress || 'n/a'} · timezone ${includeTimezone ? 'on' : 'off'} · map ${
			includeMap ? 'on' : 'off'
		}`;
	}

	async function refreshStatus(signal?: AbortSignal) {
		loadError = null;
		loading = true;
		try {
			const status = await fetchGeoStatus(client.id, { signal });
			providers = status.providers;
			provider = status.defaultProvider;
			result = status.lastLookup;
			generatedAt = status.generatedAt;
			if (status.lastLookup) {
				ipAddress = status.lastLookup.ip;
			}
		} catch (err) {
			loadError = (err as Error).message ?? 'Failed to load geolocation status';
		} finally {
			loading = false;
		}
	}

	function recordDraft() {
		if (!ipAddress.trim()) {
			loadError = 'IP address is required to stage a lookup';
			return;
		}
		loadError = null;
		log = appendWorkspaceLog(
			log,
			createWorkspaceLogEntry('Geolocation lookup staged', describePlan(), 'draft')
		);
	}

	async function lookup() {
		const ip = ipAddress.trim();
		if (!ip) {
			loadError = 'IP address is required for lookup';
			return;
		}

		loadError = null;
		saving = true;
		const detail = describePlan();
		try {
			const resolved = await lookupGeoMetadata(client.id, {
				ip,
				provider,
				includeTimezone,
				includeMap
			});
			result = resolved;
			generatedAt = resolved.retrievedAt;
			log = appendWorkspaceLog(
				log,
				createWorkspaceLogEntry('Geolocation lookup complete', detail, 'complete')
			);
		} catch (err) {
			const message = (err as Error).message ?? 'Failed to resolve IP metadata';
			loadError = message;
			log = appendWorkspaceLog(
				log,
				createWorkspaceLogEntry('Geolocation lookup failed', message, 'failed')
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
				<CardTitle class="text-base">Lookup configuration</CardTitle>
				<CardDescription>Adjust provider and enrichment options.</CardDescription>
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
					<Label for="geo-ip">IP address</Label>
					<Input id="geo-ip" bind:value={ipAddress} placeholder="203.0.113.10" disabled={saving} />
				</div>
				<div class="grid gap-2">
					<Label for="geo-provider">Provider</Label>
					<Select
						type="single"
						value={provider}
						onValueChange={(value) => (provider = value as GeoProvider)}
						disabled={saving}
					>
						<SelectTrigger id="geo-provider" class="w-full">
							<span class="uppercase">{provider}</span>
						</SelectTrigger>
						<SelectContent>
							{#each providers as option (option)}
								<SelectItem value={option}>{option.toUpperCase()}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</div>
				<div class="grid gap-2">
					<Label for="geo-cache">Cache duration (hours)</Label>
					<Input
						id="geo-cache"
						type="number"
						min={1}
						step={1}
						bind:value={cacheHours}
						disabled={saving}
					/>
				</div>
			</div>
			<div class="grid gap-4 md:grid-cols-2">
				<label
					class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
				>
					<div>
						<p class="text-sm font-medium text-foreground">Include timezone</p>
						<p class="text-xs text-muted-foreground">Add timezone and offset metadata</p>
					</div>
					<Switch bind:checked={includeTimezone} disabled={saving} />
				</label>
				<label
					class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
				>
					<div>
						<p class="text-sm font-medium text-foreground">Render map preview</p>
						<p class="text-xs text-muted-foreground">
							Display static map with approximate location
						</p>
					</div>
					<Switch bind:checked={includeMap} disabled={saving} />
				</label>
			</div>
		</CardContent>
		<CardFooter class="flex flex-wrap gap-3">
			<Button type="button" variant="outline" onclick={recordDraft} disabled={saving}
				>Save draft</Button
			>
			<Button type="button" onclick={lookup} disabled={saving}
				>{saving ? 'Resolving…' : 'Queue lookup'}</Button
			>
		</CardFooter>
	</Card>

	<Card class="border-dashed">
		<CardHeader>
			<CardTitle class="text-base">Lookup result</CardTitle>
			<CardDescription>
				Details from the most recent lookup.
				{#if result}
					<span class="ml-2 text-xs text-muted-foreground">Retrieved {result.retrievedAt}</span>
				{:else if generatedAt}
					<span class="ml-2 text-xs text-muted-foreground">Last updated {generatedAt}</span>
				{/if}
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-3 text-sm">
			{#if loading}
				<p class="rounded-lg border border-border/40 bg-muted/30 p-3 text-muted-foreground">
					Loading latest lookup…
				</p>
			{:else if !result}
				<p class="rounded-lg border border-border/60 bg-muted/30 p-3 text-muted-foreground">
					No lookup has been performed yet.
				</p>
			{:else}
				<div class="rounded-lg border border-border/60 bg-muted/30 p-4">
					<p class="text-sm font-medium text-foreground">
						{result.city ? `${result.city}, ` : ''}{result.region
							? `${result.region}, `
							: ''}{result.country}
					</p>
					<p class="text-xs text-muted-foreground">
						Coordinates: {result.latitude.toFixed(4)}°, {result.longitude.toFixed(4)}° · Network: {result.networkType}
					</p>
					{#if result.timezone}
						<p class="text-xs text-muted-foreground">
							Timezone: {result.timezone.id} ({result.timezone
								.offset}{#if result.timezone.abbreviation}
								· {result.timezone.abbreviation}{/if})
						</p>
					{/if}
					{#if result.isp || result.asn}
						<p class="text-xs text-muted-foreground">
							{#if result.isp}ISP: {result.isp}{/if}
							{#if result.asn}
								{result.isp ? ' · ' : ''}ASN: {result.asn}
							{/if}
						</p>
					{/if}
					<p class="text-xs text-muted-foreground">
						Provider: {result.provider.toUpperCase()} · IP: {result.ip}
					</p>
				</div>
				{#if includeMap && result.mapUrl}
					<a
						class="block rounded-lg border border-border/60 bg-muted/30 p-4 text-sm text-foreground hover:bg-muted/50"
						href={resolve(result.mapUrl as any)}
						target="_blank"
						rel="noreferrer"
					>
						Open map preview
					</a>
				{/if}
			{/if}
		</CardContent>
	</Card>

	{#if log.length > 0}
		<Card>
			<CardHeader>
				<CardTitle class="text-base">Activity</CardTitle>
				<CardDescription>Geolocation lookup history for this session.</CardDescription>
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
</div>
