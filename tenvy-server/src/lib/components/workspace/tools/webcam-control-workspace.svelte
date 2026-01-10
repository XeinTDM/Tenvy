<script lang="ts">
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
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { getClientTool } from '$lib/data/client-tools';
	import type { Client } from '$lib/data/clients';
	import { appendWorkspaceLog, createWorkspaceLogEntry } from '$lib/workspace/utils';
	import type { WorkspaceLogEntry } from '$lib/workspace/types';
	import { notifyToolActivationCommand } from '$lib/utils/agent-commands.js';
	import type {
		WebcamDevice,
		WebcamDeviceInventoryState,
		WebcamSessionState
	} from '$lib/types/webcam';

	const { client } = $props<{ client: Client }>();

	const tool = getClientTool('webcam-control');
	void tool;

	const apiBase = `/api/agents/${encodeURIComponent(client.id)}/webcam` as const;

	let inventoryPending = $state(false);
	let inventoryLoading = $state(false);
	let devices = $state<WebcamDevice[]>([]);
	let selectedDevice = $state('');
	let session = $state<WebcamSessionState | null>(null);
	let sessionLoading = $state(false);
	let errorMessage = $state<string | null>(null);
	let log = $state<WorkspaceLogEntry[]>([]);
	let infoMessage = $state<string | null>(null);

	let videoElement: HTMLVideoElement | null = null;

	function logAction(
		action: string,
		detail: string,
		status: WorkspaceLogEntry['status'] = 'complete'
	) {
		log = appendWorkspaceLog(log, createWorkspaceLogEntry(action, detail, status));
		notifyToolActivationCommand(client.id, 'webcam-control', {
			action: `event:${action}`,
			metadata: { detail, status }
		});
	}

	async function fetchInventory() {
		inventoryLoading = true;
		errorMessage = null;
		infoMessage = null;
		try {
			const response = await fetch(`${apiBase}/devices`, {
				method: 'GET',
				headers: { Accept: 'application/json' }
			});
			if (!response.ok) {
				throw new Error(`Inventory request failed (${response.status})`);
			}
			const payload = (await response.json()) as WebcamDeviceInventoryState;
			inventoryPending = Boolean(payload.pending);
			devices = payload.inventory?.devices ?? [];
			if (!selectedDevice && devices.length > 0) {
				selectedDevice = devices[0]?.id ?? '';
			}
			if (devices.length === 0 && !inventoryPending) {
				infoMessage = 'No webcams reported by the remote agent.';
			}
		} catch (err) {
			const message = err instanceof Error ? err.message : 'Failed to load webcam inventory.';
			errorMessage = message;
			logAction('Inventory fetch failed', message, 'draft');
		} finally {
			inventoryLoading = false;
		}
	}

	async function requestInventoryRefresh() {
		errorMessage = null;
		infoMessage = null;
		try {
			const response = await fetch(`${apiBase}/devices/refresh`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', Accept: 'application/json' }
			});
			if (!response.ok) {
				throw new Error(`Refresh failed (${response.status})`);
			}
			inventoryPending = true;
			logAction(
				'Inventory refresh requested',
				'Webcam inventory refresh command queued.',
				'in-progress'
			);
		} catch (err) {
			const message = err instanceof Error ? err.message : 'Unable to refresh webcam inventory.';
			errorMessage = message;
			logAction('Inventory refresh failed', message, 'draft');
		}
	}

	async function startSession() {
		if (sessionLoading) {
			return;
		}
		if (!selectedDevice) {
			errorMessage = 'Select a webcam before starting the stream.';
			logAction('Webcam start rejected', 'No webcam selected.', 'draft');
			return;
		}

		sessionLoading = true;
		errorMessage = null;
		infoMessage = null;

		try {
			const response = await fetch(`${apiBase}/sessions`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
				body: JSON.stringify({ deviceId: selectedDevice })
			});
			if (!response.ok) {
				throw new Error(`Session start failed (${response.status})`);
			}
			const payload = (await response.json()) as WebcamSessionState;
			session = payload;
			logAction(
				'Webcam session requested',
				`Stream request queued for ${selectedDevice}`,
				'in-progress'
			);
			infoMessage =
				'Webcam streaming is queued. The connected agent will begin streaming when the capability is available.';
		} catch (err) {
			const message =
				err instanceof Error ? err.message : 'Unable to start the remote webcam session.';
			errorMessage = message;
			logAction('Webcam session failed', message, 'draft');
		} finally {
			sessionLoading = false;
		}
	}

	async function stopSession() {
		if (sessionLoading || !session) {
			return;
		}
		sessionLoading = true;
		try {
			const response = await fetch(`${apiBase}/sessions/${encodeURIComponent(session.sessionId)}`, {
				method: 'DELETE',
				headers: { Accept: 'application/json' }
			});
			if (!response.ok) {
				throw new Error(`Session stop failed (${response.status})`);
			}
			session = null;
			infoMessage = 'Webcam session closed. Await further commands to resume streaming.';
			logAction('Webcam session stopped', 'Remote webcam session terminated.');
		} catch (err) {
			const message =
				err instanceof Error ? err.message : 'Unable to stop the remote webcam session.';
			errorMessage = message;
			logAction('Webcam stop failed', message, 'draft');
		} finally {
			sessionLoading = false;
		}
	}

	onMount(() => {
		void fetchInventory();
	});
</script>

<Card class="flex flex-col gap-4">
	<CardHeader>
		<CardTitle>Webcam Control</CardTitle>
		<CardDescription>
			Manage remote webcam enumeration and streaming requests via the connected agent.
		</CardDescription>
	</CardHeader>
	<CardContent class="flex flex-col gap-6">
		<section class="flex flex-col gap-3">
			<div class="flex flex-wrap items-end gap-3">
				<div class="flex flex-col gap-1">
					<Label for="webcam-device">Camera</Label>
					{#if devices.length > 0}
						<Select type="single" bind:value={selectedDevice}>
							<SelectTrigger id="webcam-device" class="w-64">
								{#if selectedDevice}
									{devices.find((device) => device.id === selectedDevice)?.label ??
										'Select a camera'}
								{:else}
									Select a camera
								{/if}
							</SelectTrigger>
							<SelectContent>
								{#each devices as device (device.id)}
									<SelectItem value={device.id}>{device.label}</SelectItem>
								{/each}
							</SelectContent>
						</Select>
					{:else}
						<Input id="webcam-device" value="No webcams detected" readonly disabled class="w-64" />
					{/if}
				</div>
				<div class="flex gap-2">
					<Button variant="secondary" disabled={inventoryLoading} onclick={fetchInventory}>
						Refresh status
					</Button>
					<Button variant="outline" disabled={inventoryLoading} onclick={requestInventoryRefresh}>
						Request agent refresh
					</Button>
				</div>
			</div>
			{#if inventoryPending}
				<p class="text-sm text-muted-foreground">
					Awaiting an updated device inventory from the agent.
				</p>
			{/if}
			{#if infoMessage}
				<p class="text-sm text-muted-foreground">{infoMessage}</p>
			{/if}
			{#if errorMessage}
				<p class="text-sm text-destructive">{errorMessage}</p>
			{/if}
		</section>

		<section class="flex flex-col gap-4">
			<div class="flex gap-2">
				<Button onclick={startSession} disabled={sessionLoading || !selectedDevice}>
					{session ? 'Restart stream' : 'Start stream'}
				</Button>
				<Button variant="outline" onclick={stopSession} disabled={sessionLoading || !session}>
					Stop stream
				</Button>
			</div>
			<div class="rounded-lg border bg-muted/40 p-4">
				<video
					bind:this={videoElement}
					class="aspect-video w-full rounded-lg bg-black"
					autoplay
					playsinline
					muted
				></video>
				<p class="mt-2 text-sm text-muted-foreground">
					Remote webcam streaming will appear here once the agent begins transmitting media. Until
					then, commands queue successfully but playback may remain unavailable if the agent does
					not support webcam streaming yet.
				</p>
			</div>
		</section>

		<section class="flex flex-col gap-2">
			<h3 class="text-sm font-semibold">Activity log</h3>
			{#if log.length === 0}
				<p class="text-sm text-muted-foreground">No actions recorded for this session yet.</p>
			{:else}
				<ul class="flex flex-col gap-1 text-sm">
					{#each log as entry (entry.id)}
						<li>
							<span class="font-medium">{entry.action}</span>
							<span class="mx-2 text-muted-foreground">—</span>
							<span>{entry.detail}</span>
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	</CardContent>
</Card>
