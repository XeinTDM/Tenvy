<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import {
		Card,
		CardContent,
		CardDescription,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert/index.js';
	import { RefreshCw, TriangleAlert } from '@lucide/svelte';
	import WorkspaceHeroHeader from '$lib/components/workspace/WorkspaceHeroHeader.svelte';
	import { getClientTool } from '$lib/data/client-tools';
	import type { Client } from '$lib/data/clients';
	import type {
		SystemInfoCPU,
		SystemInfoNetworkInterface,
		SystemInfoSnapshot,
		SystemInfoStorage
	} from '$lib/types/system-info';

	const { client, surface = 'workspace' } = $props<{
		client: Client;
		surface?: 'workspace' | 'dialog';
	}>();

	const tool = $derived(getClientTool('system-info'));

	let snapshot = $state<SystemInfoSnapshot | null>(null);
	let loading = $state(false);
	let errorMessage = $state<string | null>(null);

	const dateTimeFormatter = new Intl.DateTimeFormat(undefined, {
		dateStyle: 'medium',
		timeStyle: 'medium'
	});
	const bytesFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });
	const integerFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 });
	const percentFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });
	const frequencyFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });

	const lastCollectedLabel = $derived(() => formatTimestamp(snapshot?.report.collectedAt));
	const lastReceivedLabel = $derived(() => formatTimestamp(snapshot?.receivedAt));

	const heroMetadata = $derived(() => {
		if (surface !== 'workspace') {
			return [];
		}
		return [
			{
				label: 'Collected',
				value: lastCollectedLabel ?? 'Pending',
				hint: snapshot ? 'Agent inventory timestamp' : 'Awaiting snapshot'
			},
			{
				label: 'Received',
				value: lastReceivedLabel ?? 'Pending',
				hint: snapshot ? 'Controller receipt time' : 'Pending update'
			},
			{
				label: 'Agent version',
				value: snapshot?.report.agent.version ?? client.version ?? '—',
				hint: `Codename ${client.codename}`
			}
		];
	}) as unknown as { label: string; value: string; hint?: string }[];

	async function loadSnapshot(refresh = false) {
		loading = true;
		if (!refresh) {
			errorMessage = null;
		}
		try {
			const query = refresh ? '?refresh=true' : '';
			const response = await fetch(`/api/agents/${client.id}/system-info${query}`);
			if (!response.ok) {
				const message =
					(await response.text())?.trim() || 'Failed to load system information snapshot';
				throw new Error(message);
			}
			const data = (await response.json()) as SystemInfoSnapshot;
			snapshot = data;
			errorMessage = null;
		} catch (err) {
			const message =
				err instanceof Error ? err.message : 'Failed to load system information snapshot';
			errorMessage = message;
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		loadSnapshot();
	});

	function formatTimestamp(value?: string | null): string | null {
		if (!value) {
			return null;
		}
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) {
			return value;
		}
		return dateTimeFormatter.format(date);
	}

	function formatTimestampDisplay(value?: string | null): string {
		const formatted = formatTimestamp(value);
		return formatted ?? '—';
	}

	function formatBytes(value?: number): string {
		if (typeof value !== 'number' || !Number.isFinite(value)) {
			return '—';
		}
		if (value === 0) {
			return '0 B';
		}
		const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
		const exponent = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
		const normalized = value / 1024 ** exponent;
		const formatted =
			normalized >= 100
				? integerFormatter.format(Math.round(normalized))
				: bytesFormatter.format(normalized);
		return `${formatted} ${units[exponent]}`;
	}

	function formatPercent(value?: number): string {
		if (typeof value !== 'number' || Number.isNaN(value)) {
			return '—';
		}
		return `${percentFormatter.format(value)}%`;
	}

	function formatNumber(value?: number): string {
		if (typeof value !== 'number' || Number.isNaN(value)) {
			return '—';
		}
		return integerFormatter.format(value);
	}

	function formatDuration(seconds?: number): string {
		if (typeof seconds !== 'number' || Number.isNaN(seconds) || seconds <= 0) {
			return '—';
		}
		const units = [
			{ label: 'd', value: 86_400 },
			{ label: 'h', value: 3_600 },
			{ label: 'm', value: 60 }
		];
		let remaining = Math.floor(seconds);
		const parts: string[] = [];
		for (const unit of units) {
			if (remaining >= unit.value) {
				const amount = Math.floor(remaining / unit.value);
				parts.push(`${amount}${unit.label}`);
				remaining -= amount * unit.value;
			}
		}
		if (parts.length === 0) {
			parts.push(`${Math.max(remaining, 0)}s`);
		}
		return parts.join(' ');
	}

	function cpuLabel(cpu: SystemInfoCPU): string {
		const tokens = [cpu.vendor, cpu.model].filter((value) => !!value && value.trim().length > 0);
		return tokens.join(' ') || `CPU ${cpu.id + 1}`;
	}

	function formatFrequency(value?: number): string {
		if (typeof value !== 'number' || Number.isNaN(value)) {
			return '—';
		}
		return `${frequencyFormatter.format(value)} MHz`;
	}

	function storageCaption(storage: SystemInfoStorage): string {
		const used = formatBytes(storage.usedBytes);
		const total = formatBytes(storage.totalBytes);
		const percent = formatPercent(storage.usedPercent);
		const readOnly = storage.readOnly ? ' • Read only' : '';
		return `${used} / ${total} (${percent})${readOnly}`;
	}

	function networkAddresses(iface: SystemInfoNetworkInterface): string {
		if (!iface.addresses || iface.addresses.length === 0) {
			return '—';
		}
		return iface.addresses
			.map((address) => {
				const family = address.family ? ` (${address.family})` : '';
				return `${address.address}${family}`;
			})
			.join(', ');
	}

	const containerClasses = $derived(() =>
		surface === 'workspace'
			? 'flex flex-1 flex-col overflow-hidden rounded-2xl border border-border/60 bg-background/70 shadow-sm'
			: 'flex flex-1 flex-col'
	);

	const headerClasses = $derived(() =>
		surface === 'workspace'
			? 'flex items-center justify-between border-b border-border/60 bg-background/80 px-6 py-5'
			: 'flex items-center justify-between border-b border-border/60 bg-muted/40 px-6 py-4'
	);

	const bodyClasses = $derived(() => 'flex-1 space-y-6 overflow-auto px-6 py-5');
</script>

<div class={`flex h-full flex-col ${surface === 'workspace' ? 'gap-6' : ''}`}>
	{#if surface === 'workspace'}
		<WorkspaceHeroHeader {client} {tool} metadata={heroMetadata} />
	{/if}
	<div class={containerClasses}>
		<section class={headerClasses}>
			<div class="space-y-1">
				<h2 class="text-lg font-semibold text-foreground">System information</h2>
				<p class="text-sm text-muted-foreground">
					{#if lastCollectedLabel}
						Collected {lastCollectedLabel}
						{#if lastReceivedLabel}
							<span aria-hidden="true"> · </span>Received {lastReceivedLabel}
						{/if}
					{:else}
						Snapshot timing unavailable
					{/if}
				</p>
				<p class="text-xs text-muted-foreground">
					Client <span class="font-medium text-foreground">{client.codename}</span>
					<span aria-hidden="true"> · </span>Host {client.hostname}
					<span aria-hidden="true"> · </span>Version {client.version ?? '—'}
				</p>
			</div>
			<Button
				variant="secondary"
				class="gap-2"
				name="Refresh snapshot"
				disabled={loading}
				onclick={() => {
					void loadSnapshot(true);
				}}
			>
				<RefreshCw class={loading ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} />
				{loading ? 'Refreshing…' : 'Refresh snapshot'}
			</Button>
		</section>

		<div class={bodyClasses}>
			{#if errorMessage}
				<Alert variant="destructive">
					<TriangleAlert class="h-4 w-4" />
					<AlertTitle>Unable to load system information</AlertTitle>
					<AlertDescription>{errorMessage}</AlertDescription>
				</Alert>
			{/if}

			{#if !snapshot && loading}
				<div
					class="rounded-lg border border-dashed border-border/60 bg-muted/40 p-6 text-sm text-muted-foreground"
				>
					Collecting the latest system inventory from {client.codename}…
				</div>
			{:else if snapshot}
				{@const report = snapshot.report}
				<div class="grid gap-6 lg:grid-cols-2">
					<Card class="border-border/60">
						<CardHeader>
							<CardTitle>Host overview</CardTitle>
							<CardDescription>Resolved host identity and uptime.</CardDescription>
						</CardHeader>
						<CardContent class="grid gap-4 text-sm sm:grid-cols-2">
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Hostname
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.host.hostname ?? client.hostname}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Domain
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.host.domain ?? '—'}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									IP address
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.host.ipAddress ?? client.ip ?? '—'}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Timezone
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.host.timezone ?? '—'}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Boot time
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatTimestampDisplay(report.host.bootTime)}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Uptime
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatDuration(report.host.uptimeSeconds)}
								</p>
							</div>
						</CardContent>
					</Card>

					<Card class="border-border/60">
						<CardHeader>
							<CardTitle>Operating system</CardTitle>
							<CardDescription>Reported platform metadata from the agent.</CardDescription>
						</CardHeader>
						<CardContent class="grid gap-4 text-sm sm:grid-cols-2">
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Platform
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.os.platform ?? client.os ?? '—'}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Family
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.os.family ?? '—'}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Version
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.os.version ?? '—'}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Kernel
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.os.kernelVersion ?? '—'}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Kernel arch
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.os.kernelArch ?? report.hardware.architecture}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Running processes
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatNumber(report.os.procs)}
								</p>
							</div>
							<div class="sm:col-span-2">
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Virtualization
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.os.virtualization ?? report.hardware.virtualizationSystem ?? '—'}
								</p>
							</div>
						</CardContent>
					</Card>

					<Card class="border-border/60">
						<CardHeader>
							<CardTitle>Hardware &amp; CPU</CardTitle>
							<CardDescription
								>Detected hardware capabilities reported by the agent.</CardDescription
							>
						</CardHeader>
						<CardContent class="space-y-4 text-sm">
							<div class="grid gap-4 sm:grid-cols-2">
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Architecture
									</p>
									<p class="mt-1 font-medium text-foreground">
										{report.hardware.architecture}
									</p>
								</div>
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Logical cores
									</p>
									<p class="mt-1 font-medium text-foreground">
										{formatNumber(report.hardware.logicalCores)}
									</p>
								</div>
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Physical cores
									</p>
									<p class="mt-1 font-medium text-foreground">
										{formatNumber(report.hardware.physicalCores)}
									</p>
								</div>
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Virtualization role
									</p>
									<p class="mt-1 font-medium text-foreground">
										{report.hardware.virtualizationRole ?? '—'}
									</p>
								</div>
							</div>
							{#if report.hardware.cpus && report.hardware.cpus.length > 0}
								<div class="space-y-3">
									{#each report.hardware.cpus as cpu (cpu.id)}
										<div class="rounded-lg border border-border/60 bg-muted/30 p-3">
											<p class="text-sm font-semibold text-foreground">{cpuLabel(cpu)}</p>
											<p class="text-xs text-muted-foreground">
												{#if cpu.cores != null}
													{cpu.cores} cores
												{:else}
													—
												{/if}
												{#if cpu.mhz}
													<span aria-hidden="true"> · </span>{formatFrequency(cpu.mhz)}
												{/if}
											</p>
											<p class="text-xs text-muted-foreground">
												Cache
												{#if cpu.cacheSizeKb}
													<span aria-hidden="true"> </span>{formatNumber(cpu.cacheSizeKb)} KB
												{:else}
													<span aria-hidden="true"> </span>—
												{/if}
											</p>
										</div>
									{/each}
								</div>
							{/if}
						</CardContent>
					</Card>

					<Card class="border-border/60">
						<CardHeader>
							<CardTitle>Memory</CardTitle>
							<CardDescription>Main memory and swap allocation.</CardDescription>
						</CardHeader>
						<CardContent class="grid gap-4 text-sm sm:grid-cols-2">
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Total memory
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatBytes(report.memory.totalBytes)}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Used
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatBytes(report.memory.usedBytes)}
									<span class="text-xs text-muted-foreground">
										<span aria-hidden="true"> · </span>{formatPercent(report.memory.usedPercent)}
									</span>
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Available
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatBytes(report.memory.availableBytes)}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Swap usage
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatBytes(report.memory.swapUsedBytes)}
									<span class="text-xs text-muted-foreground">
										<span aria-hidden="true"> · </span>{formatPercent(
											report.memory.swapUsedPercent
										)}
									</span>
								</p>
							</div>
						</CardContent>
					</Card>

					{#if report.storage && report.storage.length > 0}
						<Card class="border-border/60 lg:col-span-2">
							<CardHeader>
								<CardTitle>Storage volumes</CardTitle>
								<CardDescription>Mounted filesystems visible to the agent.</CardDescription>
							</CardHeader>
							<CardContent class="overflow-x-auto">
								<table class="min-w-full text-left text-sm">
									<thead class="text-xs tracking-wide text-muted-foreground uppercase">
										<tr>
											<th class="pr-4 pb-2 font-medium">Device</th>
											<th class="pr-4 pb-2 font-medium">Mountpoint</th>
											<th class="pr-4 pb-2 font-medium">Filesystem</th>
											<th class="pr-4 pb-2 font-medium">Usage</th>
										</tr>
									</thead>
									<tbody class="divide-y divide-border/60">
										{#each report.storage as storage (storage.device)}
											<tr>
												<td class="py-2 pr-4 font-medium text-foreground">{storage.device}</td>
												<td class="py-2 pr-4 text-foreground">{storage.mountpoint}</td>
												<td class="py-2 pr-4 text-muted-foreground">{storage.filesystem ?? '—'}</td>
												<td class="py-2 pr-4 text-foreground">{storageCaption(storage)}</td>
											</tr>
										{/each}
									</tbody>
								</table>
							</CardContent>
						</Card>
					{/if}

					{#if report.network && report.network.length > 0}
						<Card class="border-border/60 lg:col-span-2">
							<CardHeader>
								<CardTitle>Network interfaces</CardTitle>
								<CardDescription>Address assignments for detected interfaces.</CardDescription>
							</CardHeader>
							<CardContent class="grid gap-3 sm:grid-cols-2">
								{#each report.network as iface (iface.name)}
									<div class="rounded-lg border border-border/60 bg-muted/30 p-3 text-sm">
										<p class="font-semibold text-foreground">{iface.name}</p>
										<p class="text-xs text-muted-foreground">
											MTU {formatNumber(iface.mtu)}
											{#if iface.macAddress}
												<span aria-hidden="true"> · </span>{iface.macAddress}
											{/if}
										</p>
										<p class="mt-2 text-xs text-muted-foreground">
											{networkAddresses(iface)}
										</p>
										{#if iface.flags && iface.flags.length > 0}
											<div class="mt-2 flex flex-wrap gap-1">
												{#each iface.flags as flag (flag)}
													<Badge variant="secondary">{flag}</Badge>
												{/each}
											</div>
										{/if}
									</div>
								{/each}
							</CardContent>
						</Card>
					{/if}

					<Card class="border-border/60">
						<CardHeader>
							<CardTitle>Runtime</CardTitle>
							<CardDescription>Process statistics and Go runtime insights.</CardDescription>
						</CardHeader>
						<CardContent class="grid gap-4 text-sm sm:grid-cols-2">
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Go runtime
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.runtime.goVersion ?? '—'}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Platform
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.runtime.goOs ?? '—'}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Architecture
								</p>
								<p class="mt-1 font-medium text-foreground">
									{report.runtime.goArch ?? '—'}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Logical CPUs
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatNumber(report.runtime.logicalCpus)}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									GOMAXPROCS
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatNumber(report.runtime.goMaxProcs)}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Goroutines
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatNumber(report.runtime.goroutines)}
								</p>
							</div>
							<div class="sm:col-span-2">
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Process
								</p>
								<p class="mt-1 font-medium text-foreground">
									PID {formatNumber(report.runtime.process?.pid)} · {report.runtime.process
										?.commandLine ?? '—'}
								</p>
								<p class="text-xs text-muted-foreground">
									Working directory {report.runtime.process?.workingDirectory ?? '—'}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Started
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatTimestampDisplay(report.runtime.process?.createTime)}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									CPU usage
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatPercent(report.runtime.process?.cpuPercent)}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									RSS memory
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatBytes(report.runtime.process?.memoryRssBytes)}
								</p>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Virtual memory
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatBytes(report.runtime.process?.memoryVmsBytes)}
								</p>
							</div>
						</CardContent>
					</Card>

					<Card class="border-border/60">
						<CardHeader>
							<CardTitle>Environment</CardTitle>
							<CardDescription>Agent execution context and PATH details.</CardDescription>
						</CardHeader>
						<CardContent class="space-y-4 text-sm">
							<div class="grid gap-4 sm:grid-cols-2">
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Username
									</p>
									<p class="mt-1 font-medium text-foreground">
										{report.environment.username ?? '—'}
									</p>
								</div>
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Home directory
									</p>
									<p class="mt-1 font-medium text-foreground">
										{report.environment.homeDir ?? '—'}
									</p>
								</div>
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Shell
									</p>
									<p class="mt-1 font-medium text-foreground">
										{report.environment.shell ?? '—'}
									</p>
								</div>
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Locale
									</p>
									<p class="mt-1 font-medium text-foreground">
										{report.environment.lang ?? '—'}
									</p>
								</div>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									PATH entries
								</p>
								<div class="mt-2 flex flex-wrap gap-1">
									{#if report.environment.pathEntries && report.environment.pathEntries.length > 0}
										{#each report.environment.pathEntries as entry (entry)}
											<Badge variant="outline">{entry}</Badge>
										{/each}
									{:else}
										<span class="text-muted-foreground">—</span>
									{/if}
								</div>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Environment variables
								</p>
								<p class="mt-1 font-medium text-foreground">
									{formatNumber(report.environment.environmentCount)} values
								</p>
							</div>
						</CardContent>
					</Card>

					<Card class="border-border/60">
						<CardHeader>
							<CardTitle>Agent metadata</CardTitle>
							<CardDescription>Agent versioning and identification.</CardDescription>
						</CardHeader>
						<CardContent class="space-y-4 text-sm">
							<div class="grid gap-4 sm:grid-cols-2">
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Agent ID
									</p>
									<p class="mt-1 font-medium text-foreground">{report.agent.id ?? client.id}</p>
								</div>
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Version
									</p>
									<p class="mt-1 font-medium text-foreground">
										{report.agent.version ?? client.version ?? '—'}
									</p>
								</div>
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Started
									</p>
									<p class="mt-1 font-medium text-foreground">
										{formatTimestampDisplay(report.agent.startTime)}
									</p>
								</div>
								<div>
									<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
										Uptime
									</p>
									<p class="mt-1 font-medium text-foreground">
										{formatDuration(report.agent.uptimeSeconds)}
									</p>
								</div>
							</div>
							<div>
								<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">
									Tags
								</p>
								<div class="mt-2 flex flex-wrap gap-1">
									{#if report.agent.tags && report.agent.tags.length > 0}
										{#each report.agent.tags as tag (tag)}
											<Badge>{tag}</Badge>
										{/each}
									{:else}
										<span class="text-muted-foreground">—</span>
									{/if}
								</div>
							</div>
						</CardContent>
					</Card>
				</div>

				{#if report.warnings && report.warnings.length > 0}
					<Alert class="border-amber-200 bg-amber-50 text-amber-900">
						<TriangleAlert class="h-4 w-4" />
						<AlertTitle>Agent warnings</AlertTitle>
						<AlertDescription>
							<ul class="list-disc space-y-1 pl-4">
								{#each report.warnings as warning (warning)}
									<li>{warning}</li>
								{/each}
							</ul>
						</AlertDescription>
					</Alert>
				{/if}
			{:else}
				<div
					class="rounded-lg border border-dashed border-border/60 bg-muted/40 p-6 text-sm text-muted-foreground"
				>
					System information has not been collected yet. Use the refresh action to request a
					snapshot.
				</div>
			{/if}
		</div>
	</div>
</div>
