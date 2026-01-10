<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
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
	import WorkspaceHeroHeader from '$lib/components/workspace/WorkspaceHeroHeader.svelte';
	import { getClientTool } from '$lib/data/client-tools';
	import type { Client } from '$lib/data/clients';
	import type {
		AudioDeviceDescriptor,
		AudioDeviceInventory,
		AudioSessionState,
		AudioStreamChunk,
		AudioUploadTrack
	} from '$lib/types/audio';
	import { appendWorkspaceLog, createWorkspaceLogEntry } from '$lib/workspace/utils';
	import type { WorkspaceLogEntry } from '$lib/workspace/types';

	const { client } = $props<{ client: Client }>();

	const tool = getClientTool('audio-control');
	const isBrowser = typeof window !== 'undefined';

	let inventory = $state<AudioDeviceInventory | null>(null);
	let pendingInventory = $state(false);
	let refreshingInventory = $state(false);
	let inventoryError = $state<string | null>(null);

	let session = $state<AudioSessionState | null>(null);
	let listening = $state(false);
	let sessionError = $state<string | null>(null);
	let playbackError = $state<string | null>(null);

	let log = $state<WorkspaceLogEntry[]>([]);
	let uploads = $state<AudioUploadTrack[]>([]);
	let uploadsError = $state<string | null>(null);
	let uploading = $state(false);

	let selectedInputId = $state<string | null>(null);
	let selectedOutputId = $state<string | null>(null);
	let selectedTrackId = $state<string | null>(null);

	let playbackStatus = $state<'idle' | 'playing' | 'paused'>('idle');
	let playbackStatusDetail = $state<string | null>(null);
	let playbackCommandError = $state<string | null>(null);

	let loopTrack = $state(true);
	let chaosMode = $state(false);
	let rickrollInsurance = $state(true);
	let playbackVolume = $state(0.8);

	let fileInput: HTMLInputElement | null = null;

	const selectedInput = () =>
		inventory?.inputs.find((device) => device.id === selectedInputId) ?? null;
	const selectedOutput = () =>
		inventory?.outputs.find((device) => device.id === selectedOutputId) ?? null;
	const selectedTrack = () => uploads.find((track) => track.id === selectedTrackId) ?? null;

	const mischiefMeter = $derived(() => {
		const points = (uploads.length > 0 ? 1 : 0) + (chaosMode ? 2 : 0) + (rickrollInsurance ? 1 : 0);
		if (points >= 3) {
			return 'Maximum hijinks armed';
		}
		if (points === 2) {
			return 'Chaotic neutral vibes';
		}
		if (points === 1) {
			return 'Mischief warming up';
		}
		return 'Serious operator detected';
	}) as unknown as string;

	const heroMetadata = $derived([
		{
			label: 'Inputs discovered',
			value: inventory ? inventory.inputs.length.toString() : pendingInventory ? 'Pending' : '0'
		},
		{
			label: 'Session state',
			value: session ? (session.active ? 'Active' : 'Stopped') : 'Idle',
			hint: listening ? 'Streaming live microphone audio to the controller.' : undefined
		},
		{
			label: 'Uploaded bops',
			value: uploads.length ? uploads.length.toString() : '0',
			hint: uploads.length ? mischiefMeter : undefined
		}
	]) as unknown as { label: string; value: string; hint?: string }[];

	let eventSource = $state<EventSource | null>(null);
	let audioContext = $state<AudioContext | null>(null);
	let playbackQueueTime = $state(0);
	let lastAutoInventoryRequestAt = $state<number | null>(null);

	const inventoryHasDevices = (value: AudioDeviceInventory | null) =>
		Boolean(value && (value.inputs.length > 0 || value.outputs.length > 0));

	async function maybeAutoRequestInventory() {
		if (!isBrowser) {
			return;
		}
		if (refreshingInventory || pendingInventory) {
			return;
		}
		if (inventoryHasDevices(inventory)) {
			lastAutoInventoryRequestAt = null;
			return;
		}
		if (lastAutoInventoryRequestAt && Date.now() - lastAutoInventoryRequestAt < 60_000) {
			return;
		}
		lastAutoInventoryRequestAt = Date.now();
		await refreshInventory();
	}

	function logAction(
		action: string,
		detail: string,
		status: WorkspaceLogEntry['status'] = 'complete'
	) {
		log = appendWorkspaceLog(log, createWorkspaceLogEntry(action, detail, status));
	}

	function setPlaybackStatus(state: 'idle' | 'playing' | 'paused', detail?: string) {
		playbackStatus = state;
		playbackStatusDetail = detail ?? null;
	}

	async function loadUploads() {
		if (!isBrowser) {
			return;
		}
		try {
			const response = await fetch(`/api/agents/${client.id}/audio/uploads`);
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			const body = (await response.json()) as { uploads?: AudioUploadTrack[] };
			uploads = body.uploads ?? [];
			uploadsError = null;
			if (uploads.length > 0) {
				if (!selectedTrackId || !uploads.some((item) => item.id === selectedTrackId)) {
					selectedTrackId = uploads[0]?.id ?? null;
				}
			} else {
				selectedTrackId = null;
			}
		} catch (err) {
			uploadsError = (err as Error).message ?? 'Failed to load audio uploads.';
		}
	}

	async function uploadTrack(file: File) {
		if (!isBrowser) {
			return;
		}
		uploading = true;
		uploadsError = null;
		try {
			const formData = new FormData();
			formData.append('file', file);
			const response = await fetch(`/api/agents/${client.id}/audio/uploads`, {
				method: 'POST',
				body: formData
			});
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			const body = (await response.json()) as {
				track?: AudioUploadTrack;
				uploads?: AudioUploadTrack[];
			};
			uploads = body.uploads ?? (body.track ? [...uploads, body.track] : uploads);
			if (body.track) {
				selectedTrackId = body.track.id;
				logAction('Uploaded audio track', body.track.originalName);
			}
			if (!selectedTrackId && uploads.length > 0) {
				selectedTrackId = uploads[0].id;
			}
		} catch (err) {
			const message = (err as Error).message ?? 'Failed to upload audio track.';
			uploadsError = message;
			logAction('Audio upload failed', message, 'failed');
		} finally {
			uploading = false;
			if (fileInput) {
				fileInput.value = '';
			}
		}
	}

	function handleFileSelection(event: Event) {
		const target = event.currentTarget as HTMLInputElement | null;
		const file = target?.files?.[0];
		if (file) {
			void uploadTrack(file);
		}
	}

	async function removeUploadTrack(track: AudioUploadTrack) {
		if (!isBrowser) {
			return;
		}
		try {
			const response = await fetch(`/api/agents/${client.id}/audio/uploads`, {
				method: 'DELETE',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ trackId: track.id })
			});
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			const body = (await response.json()) as { uploads?: AudioUploadTrack[] };
			uploads = body.uploads ?? [];
			if (!uploads.some((item) => item.id === selectedTrackId)) {
				selectedTrackId = uploads[0]?.id ?? null;
			}
			setPlaybackStatus('idle');
			playbackCommandError = null;
			logAction('Removed uploaded track', track.originalName);
		} catch (err) {
			const message = (err as Error).message ?? 'Failed to remove audio track.';
			uploadsError = message;
			logAction('Audio removal failed', message, 'failed');
		}
	}

	async function removeSelectedTrack() {
		const track = selectedTrack();
		if (!track) {
			return;
		}
		await removeUploadTrack(track);
	}

	async function playSelectedTrack() {
		if (!isBrowser) {
			return;
		}
		const track = selectedTrack();
		if (!track) {
			playbackCommandError = 'Select a track to play.';
			return;
		}
		const output = selectedOutput();
		if (!output) {
			playbackCommandError = 'Select an output device.';
			return;
		}
		playbackCommandError = null;
		try {
			const response = await fetch(`/api/agents/${client.id}/audio/playback`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					intent: 'play',
					trackId: track.id,
					deviceId: output.id,
					deviceLabel: output.label,
					volume: playbackVolume,
					loop: loopTrack,
					chaosMode,
					rickroll: rickrollInsurance
				})
			});
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			setPlaybackStatus('playing', `${track.originalName} → ${output.label ?? output.id}`);
			logAction('Playback armed', `${track.originalName} on ${output.label ?? 'output'}`);
		} catch (err) {
			playbackCommandError = (err as Error).message ?? 'Failed to start playback.';
			setPlaybackStatus('idle');
			logAction('Playback command failed', playbackCommandError ?? '', 'failed');
		}
	}

	async function pauseSelectedTrack() {
		if (!isBrowser) {
			return;
		}
		const track = selectedTrack();
		if (!track) {
			return;
		}
		try {
			const response = await fetch(`/api/agents/${client.id}/audio/playback`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ intent: 'pause', trackId: track.id })
			});
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			setPlaybackStatus('paused', track.originalName);
			logAction('Playback paused', track.originalName);
		} catch (err) {
			playbackCommandError = (err as Error).message ?? 'Failed to pause playback.';
			logAction('Playback pause failed', playbackCommandError ?? '', 'failed');
		}
	}

	async function resumeSelectedTrack() {
		if (!isBrowser) {
			return;
		}
		const track = selectedTrack();
		if (!track) {
			return;
		}
		try {
			const response = await fetch(`/api/agents/${client.id}/audio/playback`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ intent: 'resume', trackId: track.id })
			});
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			setPlaybackStatus('playing', track.originalName);
			logAction('Playback resumed', track.originalName);
		} catch (err) {
			playbackCommandError = (err as Error).message ?? 'Failed to resume playback.';
			logAction('Playback resume failed', playbackCommandError ?? '', 'failed');
		}
	}

	async function stopSelectedTrack() {
		if (!isBrowser) {
			return;
		}
		const track = selectedTrack();
		if (!track) {
			return;
		}
		try {
			const response = await fetch(`/api/agents/${client.id}/audio/playback`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ intent: 'stop', trackId: track.id })
			});
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			setPlaybackStatus('idle');
			logAction('Playback stopped', track.originalName);
		} catch (err) {
			playbackCommandError = (err as Error).message ?? 'Failed to stop playback.';
			logAction('Playback stop failed', playbackCommandError ?? '', 'failed');
		}
	}

	function randomizeChaos() {
		loopTrack = Math.random() > 0.3;
		chaosMode = Math.random() > 0.5;
		rickrollInsurance = Math.random() > 0.2;
		playbackVolume = Math.round((0.4 + Math.random() * 0.6) * 100) / 100;
		const detail = `loop=${loopTrack ? 'on' : 'off'} · chaos=${chaosMode ? 'enabled' : 'tame'} · rickroll=${
			rickrollInsurance ? 'armed' : 'disarmed'
		}`;
		logAction('Chaos preset shuffled', detail);
	}

	function promptUploadDialog() {
		fileInput?.click();
	}

	async function listenToSelectedSource() {
		const device = selectedInput() ?? selectedOutput();
		if (!device) {
			sessionError = 'Select a capture source first.';
			return;
		}
		await startListening(device);
	}

	function selectInputDevice(device: AudioDeviceDescriptor) {
		selectedInputId = device.id;
		selectedOutputId = null;
	}

	function selectOutputDevice(device: AudioDeviceDescriptor) {
		selectedOutputId = device.id;
		selectedInputId = null;
	}

	async function startListeningFromDevice(device: AudioDeviceDescriptor) {
		if (device.kind === 'input') {
			selectInputDevice(device);
		} else {
			selectOutputDevice(device);
		}
		await startListening(device);
	}

	function stopListeningFromDevice() {
		void stopListening(false);
	}

	async function loadInventory() {
		if (!isBrowser) {
			return;
		}
		try {
			const response = await fetch(`/api/agents/${client.id}/audio/devices`);
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			const body = (await response.json()) as {
				inventory?: AudioDeviceInventory | null;
				pending?: boolean;
			};
			inventory = body.inventory ?? null;
			pendingInventory = Boolean(body.pending);
			inventoryError = null;
			if (inventory) {
				if (inventory.inputs.length > 0) {
					if (!selectedInputId || !inventory.inputs.some((item) => item.id === selectedInputId)) {
						selectedInputId = inventory.inputs[0]?.id ?? null;
					}
				} else {
					selectedInputId = null;
				}
				if (inventory.outputs.length > 0) {
					if (
						!selectedOutputId ||
						!inventory.outputs.some((item) => item.id === selectedOutputId)
					) {
						selectedOutputId = inventory.outputs[0]?.id ?? null;
					}
				} else {
					selectedOutputId = null;
				}
				if (inventoryHasDevices(inventory)) {
					lastAutoInventoryRequestAt = null;
				}
			}
		} catch (err) {
			inventoryError = (err as Error).message ?? 'Failed to load audio inventory.';
		}
	}

	async function pollInventory(requestId: string) {
		const deadline = Date.now() + 15_000;
		while (Date.now() < deadline) {
			await new Promise((resolve) => setTimeout(resolve, 1000));
			await loadInventory();
			if (inventory && (!inventory.requestId || inventory.requestId === requestId)) {
				pendingInventory = false;
				logAction('Audio inventory updated', inventory.capturedAt);
				return;
			}
		}
		logAction('Audio inventory refresh timed out', requestId, 'failed');
	}

	async function refreshInventory() {
		if (!isBrowser || refreshingInventory) {
			return;
		}
		refreshingInventory = true;
		inventoryError = null;
		try {
			const response = await fetch(`/api/agents/${client.id}/audio/devices/refresh`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' }
			});
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			const body = (await response.json()) as { requestId: string };
			pendingInventory = true;
			logAction('Requested audio inventory refresh', body.requestId, 'pending');
			await pollInventory(body.requestId);
		} catch (err) {
			inventoryError = (err as Error).message ?? 'Failed to refresh audio inventory.';
			logAction('Audio inventory refresh failed', inventoryError ?? '', 'failed');
		} finally {
			refreshingInventory = false;
		}
	}

	async function loadSessionState() {
		if (!isBrowser) {
			return;
		}
		try {
			const response = await fetch(`/api/agents/${client.id}/audio/session`);
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			const body = (await response.json()) as { session: AudioSessionState | null };
			session = body.session ?? null;
			if (session?.deviceId) {
				selectedInputId = session.deviceId;
			}
			if (session?.active) {
				listening = true;
				openStream(session.sessionId);
			} else {
				listening = false;
			}
		} catch (err) {
			sessionError = (err as Error).message ?? 'Failed to load audio session state.';
		}
	}

	async function startListening(device: AudioDeviceDescriptor) {
		if (!isBrowser) {
			return;
		}
		if (device.kind !== 'input') {
			sessionError =
				'Listening to output devices is not supported yet. Choose an input device instead.';
			return;
		}
		const label = device.label ?? device.id;
		await stopListening(true);
		sessionError = null;
		playbackError = null;
		try {
			const response = await fetch(`/api/agents/${client.id}/audio/session`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					deviceId: device.id,
					deviceLabel: device.label,
					direction: device.kind
				})
			});
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			const body = (await response.json()) as { session: AudioSessionState | null };
			session = body.session;
			if (!session) {
				throw new Error('Session was not created.');
			}
			listening = true;
			logAction('Audio session started', label);
			openStream(session.sessionId);
		} catch (err) {
			listening = false;
			sessionError = (err as Error).message ?? 'Failed to start audio session.';
			logAction('Audio session start failed', sessionError ?? '', 'failed');
		}
	}

	async function stopListening(silent = false) {
		if (!isBrowser) {
			cleanupPlayback();
			listening = false;
			return;
		}
		const current = session;
		const label = current?.deviceLabel ?? current?.sessionId ?? 'session';
		if (!current) {
			cleanupPlayback();
			listening = false;
			return;
		}
		try {
			const response = await fetch(`/api/agents/${client.id}/audio/session`, {
				method: 'DELETE',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ sessionId: current.sessionId })
			});
			if (response.ok) {
				const body = (await response.json()) as { session: AudioSessionState | null };
				session = body.session ?? null;
			}
		} catch (err) {
			if (!silent) {
				sessionError = (err as Error).message ?? 'Failed to stop audio session.';
			}
		} finally {
			cleanupPlayback();
			listening = false;
			if (!silent) {
				logAction('Audio session stopped', label);
			}
		}
	}

	function openStream(sessionId: string) {
		if (!isBrowser) {
			return;
		}
		cleanupPlayback();
		const url = new URL(`/api/agents/${client.id}/audio/stream`, window.location.origin);
		url.searchParams.set('sessionId', sessionId);
		const source = new EventSource(url.toString());
		eventSource = source;
		source.addEventListener('session', (event) => {
			const detail = JSON.parse((event as MessageEvent<string>).data) as AudioSessionState;
			session = detail;
		});
		source.addEventListener('chunk', (event) => {
			const detail = JSON.parse((event as MessageEvent<string>).data) as AudioStreamChunk;
			void handleChunk(detail);
		});
		source.addEventListener('stopped', () => {
			listening = false;
			playbackError = 'Audio session ended.';
			if (session) {
				session = { ...session, active: false } satisfies AudioSessionState;
			}
			cleanupPlayback();
		});
		source.onerror = () => {
			playbackError = 'Audio stream interrupted.';
		};
	}

	function cleanupPlayback() {
		if (eventSource) {
			eventSource.close();
			eventSource = null;
		}
		if (audioContext) {
			audioContext.close().catch(() => {});
			audioContext = null;
		}
		playbackQueueTime = 0;
	}

	async function ensureAudioContext(sampleRate: number): Promise<boolean> {
		if (!isBrowser) {
			return false;
		}
		if (!audioContext) {
			try {
				audioContext = new AudioContext();
			} catch {
				playbackError = 'Audio playback is not supported in this environment.';
				return false;
			}
			playbackQueueTime = audioContext.currentTime;
		}
		if (audioContext.state === 'suspended') {
			try {
				await audioContext.resume();
			} catch {
				// ignore resume errors
			}
		}
		// AudioBuffer resamples automatically to the context sample rate.
		void sampleRate;
		return true;
	}

	function decodePcm(data: string | Uint8Array): Int16Array | null {
		try {
			if (data instanceof Uint8Array) {
				return new Int16Array(data.buffer, data.byteOffset, data.byteLength);
			}
			const binary = atob(data);
			if (binary.length % 2 !== 0) {
				return null;
			}
			const buffer = new ArrayBuffer(binary.length);
			const bytes = new Uint8Array(buffer);
			for (let i = 0; i < binary.length; i += 1) {
				bytes[i] = binary.charCodeAt(i);
			}
			return new Int16Array(buffer);
		} catch {
			return null;
		}
	}

	function schedulePlayback(format: AudioStreamChunk['format'], pcm: Int16Array) {
		if (!audioContext) {
			return;
		}
		const channels = Math.max(1, Math.min(2, format.channels ?? 1));
		const frameCount = Math.floor(pcm.length / channels);
		if (frameCount <= 0) {
			return;
		}
		const buffer = audioContext.createBuffer(channels, frameCount, format.sampleRate);
		for (let channel = 0; channel < channels; channel += 1) {
			const channelData = buffer.getChannelData(channel);
			for (let frame = 0; frame < frameCount; frame += 1) {
				const sampleIndex = frame * channels + channel;
				const value = pcm[sampleIndex] / 32768;
				channelData[frame] = Math.max(-1, Math.min(1, value));
			}
		}
		const source = audioContext.createBufferSource();
		source.buffer = buffer;
		source.connect(audioContext.destination);
		const startAt = Math.max(audioContext.currentTime + 0.05, playbackQueueTime);
		source.start(startAt);
		playbackQueueTime = startAt + buffer.duration;
	}

	async function handleChunk(chunk: AudioStreamChunk) {
		if (!(await ensureAudioContext(chunk.format.sampleRate))) {
			return;
		}
		const pcm = decodePcm(chunk.data);
		if (!pcm) {
			playbackError = 'Received malformed audio data.';
			return;
		}
		playbackError = null;
		if (session) {
			session = {
				...session,
				lastSequence: chunk.sequence,
				lastUpdatedAt: chunk.timestamp
			} satisfies AudioSessionState;
		}
		schedulePlayback(chunk.format, pcm);
	}

	onMount(async () => {
		if (!isBrowser) {
			return;
		}
		await loadInventory();
		void maybeAutoRequestInventory();
		await loadSessionState();
		await loadUploads();
	});

	onDestroy(() => {
		void stopListening(true);
	});
</script>

<div class="space-y-6">
	<WorkspaceHeroHeader {client} {tool} metadata={heroMetadata} />
	<div class="grid gap-6 xl:grid-cols-[1.6fr,1.4fr]">
		<Card>
			<CardHeader class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
				<div>
					<CardTitle>Agent audio bridge</CardTitle>
					<CardDescription>
						Choose a source to snoop—tap the microphone or the system output (loopback).
					</CardDescription>
				</div>
				<div class="flex items-center gap-2">
					{#if pendingInventory}
						<Badge variant="secondary">Pending update</Badge>
					{/if}
					<Button
						variant="outline"
						size="sm"
						onclick={refreshInventory}
						disabled={refreshingInventory}>Refresh devices</Button
					>
				</div>
			</CardHeader>
			<CardContent class="space-y-4">
				{#if inventoryError}
					<p class="text-sm text-destructive">{inventoryError}</p>
				{/if}
				<div class="grid gap-4 lg:grid-cols-2">
					<div class="grid gap-2">
						<Label for="audio-source-select">Capture source</Label>
						{#if inventory && (inventory.inputs.length > 0 || inventory.outputs.length > 0)}
							<Select
								type="single"
								value={selectedInputId ?? selectedOutputId ?? undefined}
								onValueChange={(value) => {
									const val = value as string;
									const isInput = inventory?.inputs.some((d) => d.id === val);
									if (isInput) {
										selectedInputId = val;
										selectedOutputId = null;
									} else {
										selectedOutputId = val;
										selectedInputId = null;
									}
								}}
							>
								<SelectTrigger id="audio-source-select" class="w-full">
									<span class="truncate">
										{selectedInput()?.label ?? selectedOutput()?.label ?? 'Select device'}
									</span>
								</SelectTrigger>
								<SelectContent>
									{#if inventory.inputs.length > 0}
										<div class="px-2 py-1.5 text-xs font-semibold text-muted-foreground">
											Microphones
										</div>
										{#each inventory.inputs as device (device.id)}
											<SelectItem value={device.id}>{device.label}</SelectItem>
										{/each}
									{/if}
									{#if inventory.outputs.length > 0}
										<div class="mt-2 px-2 py-1.5 text-xs font-semibold text-muted-foreground">
											System Outputs
										</div>
										{#each inventory.outputs as device (device.id)}
											<SelectItem value={device.id}>{device.label}</SelectItem>
										{/each}
									{/if}
								</SelectContent>
							</Select>
						{:else}
							<p class="text-sm text-muted-foreground">
								No audio devices were reported. Refresh the inventory or verify the agent was built
								with audio support.
							</p>
						{/if}
					</div>
					<div class="grid gap-2">
						<Label>Session status</Label>
						<div class="rounded-lg border border-border/60 bg-muted/40 px-3 py-2 text-sm">
							<div class="flex items-center justify-between gap-2">
								<span class="font-medium text-foreground">
									{session ? (session.deviceLabel ?? 'Unknown device') : 'Idle'}
								</span>
								{#if listening && session?.active}
									<Badge>Streaming</Badge>
								{:else if session?.active}
									<Badge variant="secondary">Pending</Badge>
								{:else}
									<Badge variant="outline">Stopped</Badge>
								{/if}
							</div>
							<p class="text-muted-foreground">
								{#if session?.sessionId}
									Session ID: {session.sessionId}
								{:else}
									No active bridge
								{/if}
							</p>
						</div>
					</div>
				</div>
				{#if sessionError}
					<p class="text-sm text-destructive">{sessionError}</p>
				{/if}
				{#if playbackError}
					<p class="text-warning-foreground text-sm">{playbackError}</p>
				{/if}
				<div class="flex flex-wrap gap-2">
					<Button
						onclick={listenToSelectedSource}
						disabled={(!selectedInput() && !selectedOutput()) || refreshingInventory}
						>{listening ? 'Listening…' : 'Start listening'}</Button
					>
					<Button
						variant="outline"
						onclick={() => stopListening(false)}
						disabled={!session?.active && !listening}
					>
						Stop session
					</Button>
					<Button variant="ghost" onclick={loadSessionState}>Sync state</Button>
				</div>
				{#if session}
					<div class="space-y-2 rounded-lg border border-dashed border-border/60 px-3 py-2 text-sm">
						<p class="text-muted-foreground">Direction: {session.direction}</p>
						<p class="text-muted-foreground">
							Format: {session.format.sampleRate} Hz · {session.format.channels}
							{session.format.channels === 1 ? 'channel' : 'channels'}
						</p>
						{#if session.lastUpdatedAt}
							<p class="text-muted-foreground">
								Last update: {new Date(session.lastUpdatedAt).toLocaleString()}
							</p>
						{/if}
					</div>
				{:else}
					<p class="text-sm text-muted-foreground">
						Kick off a session to stream the agent's microphone straight into your headset.
					</p>
				{/if}
			</CardContent>
		</Card>

		<Card>
			<CardHeader>
				<CardTitle>Output mischief lab</CardTitle>
				<CardDescription>
					Upload a track, pick a speaker, and unleash remote sonic chaos with optional troll
					toggles.
				</CardDescription>
			</CardHeader>
			<CardContent class="space-y-4">
				{#if uploadsError}
					<p class="text-sm text-destructive">{uploadsError}</p>
				{/if}
				<div class="grid gap-4 lg:grid-cols-2">
					<div class="grid gap-2">
						<Label for="audio-output-select">Output device</Label>
						{#if inventory && inventory.outputs.length > 0}
							<Select
								type="single"
								value={selectedOutputId ?? undefined}
								onValueChange={(value) => (selectedOutputId = value as string)}
							>
								<SelectTrigger id="audio-output-select" class="w-full">
									<span class="truncate">{selectedOutput()?.label ?? 'Select speaker'}</span>
								</SelectTrigger>
								<SelectContent>
									{#each inventory.outputs as device (device.id)}
										<SelectItem value={device.id}>{device.label}</SelectItem>
									{/each}
								</SelectContent>
							</Select>
						{:else}
							<p class="text-sm text-muted-foreground">
								No playback devices were reported. Refresh the inventory or verify the agent was
								built with audio support (CGO enabled).
							</p>
						{/if}
					</div>
					<div class="grid gap-2">
						<Label for="audio-volume">Volume (0-1)</Label>
						<Input
							id="audio-volume"
							type="number"
							min={0}
							max={1}
							step={0.05}
							bind:value={playbackVolume}
						/>
						<p class="text-xs text-muted-foreground">
							Fine tune how aggressively the agent gets serenaded.
						</p>
					</div>
				</div>
				<div class="grid gap-4 lg:grid-cols-[1fr,auto]">
					<div class="grid gap-2">
						<Label for="audio-track-select">Uploaded tracks</Label>
						{#if uploads.length > 0}
							<Select
								type="single"
								value={selectedTrackId ?? undefined}
								onValueChange={(value) => (selectedTrackId = value as string)}
							>
								<SelectTrigger id="audio-track-select" class="w-full">
									<span class="truncate"
										>{selectedTrack()?.originalName ?? 'Choose your weapon'}</span
									>
								</SelectTrigger>
								<SelectContent>
									{#each uploads as track (track.id)}
										<SelectItem value={track.id}>
											{track.originalName} ({Math.round(track.size / 1024)} KB)
										</SelectItem>
									{/each}
								</SelectContent>
							</Select>
						{:else}
							<p class="text-sm text-muted-foreground">
								No tracks uploaded yet. Time to prep a surprise anthem.
							</p>
						{/if}
					</div>
					<div class="flex items-end justify-end gap-2">
						<input
							class="hidden"
							type="file"
							accept="audio/*"
							bind:this={fileInput}
							onchange={handleFileSelection}
						/>
						<Button type="button" onclick={promptUploadDialog} disabled={uploading}>
							{uploading ? 'Uploading…' : 'Upload track'}
						</Button>
						<Button
							type="button"
							variant="outline"
							onclick={removeSelectedTrack}
							disabled={!selectedTrack()}
						>
							Remove
						</Button>
					</div>
				</div>
				<div class="flex flex-wrap gap-2">
					<Button onclick={playSelectedTrack} disabled={!selectedTrack() || !selectedOutput()}
						>Play</Button
					>
					<Button
						variant="outline"
						onclick={pauseSelectedTrack}
						disabled={playbackStatus !== 'playing'}>Pause</Button
					>
					<Button
						variant="outline"
						onclick={resumeSelectedTrack}
						disabled={playbackStatus !== 'paused'}>Resume</Button
					>
					<Button
						variant="destructive"
						onclick={stopSelectedTrack}
						disabled={playbackStatus === 'idle'}>Stop</Button
					>
					<Button variant="ghost" onclick={randomizeChaos}>Randomize chaos</Button>
				</div>
				<div class="grid gap-3 sm:grid-cols-3">
					<div
						class="flex items-center justify-between rounded-lg border border-border/60 px-3 py-2"
					>
						<div>
							<p class="text-sm font-medium">Loop forever</p>
							<p class="text-xs text-muted-foreground">Never let them escape the banger.</p>
						</div>
						<Switch bind:checked={loopTrack} />
					</div>
					<div
						class="flex items-center justify-between rounded-lg border border-border/60 px-3 py-2"
					>
						<div>
							<p class="text-sm font-medium">Chaos mode</p>
							<p class="text-xs text-muted-foreground">
								Random offsets, spooky panning, the works.
							</p>
						</div>
						<Switch bind:checked={chaosMode} />
					</div>
					<div
						class="flex items-center justify-between rounded-lg border border-border/60 px-3 py-2"
					>
						<div>
							<p class="text-sm font-medium">Rickroll insurance</p>
							<p class="text-xs text-muted-foreground">Auto-inject meme backup on failure.</p>
						</div>
						<Switch bind:checked={rickrollInsurance} />
					</div>
				</div>
				{#if playbackCommandError}
					<p class="text-sm text-destructive">{playbackCommandError}</p>
				{/if}
				{#if playbackStatus !== 'idle'}
					<p class="text-sm text-muted-foreground">
						{playbackStatus === 'playing' ? 'Playing' : 'Paused'}: {playbackStatusDetail ??
							'unknown track'}.
					</p>
				{:else if selectedTrack()}
					<p class="text-sm text-muted-foreground">
						Ready to blast {selectedTrack()?.originalName} at {selectedOutput()?.label ??
							'the agent'}.
					</p>
				{/if}
			</CardContent>
			<CardFooter class="flex items-center justify-between">
				<span class="text-xs text-muted-foreground">{mischiefMeter}</span>
				<Badge variant="outline">{uploads.length} track{uploads.length === 1 ? '' : 's'}</Badge>
			</CardFooter>
		</Card>
	</div>

	<Card>
		<CardHeader>
			<CardTitle>Discovered audio hardware</CardTitle>
			<CardDescription>
				Full inventory of inputs and outputs as reported by the agent during the last scan.
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-6">
			{#if inventoryError}
				<p class="text-sm text-destructive">{inventoryError}</p>
			{/if}
			{#if !inventory}
				<p class="text-sm text-muted-foreground">
					No inventory is currently available. Request a refresh to collect the agent's audio
					hardware list.
				</p>
			{:else}
				<div class="space-y-4">
					<div class="space-y-2">
						<h3 class="text-sm font-medium text-foreground/80">Input devices</h3>
						{#if inventory.inputs.length === 0}
							<p class="text-sm text-muted-foreground">
								No audio inputs were reported. Refresh the inventory or ensure the agent was built
								with audio support (CGO enabled).
							</p>
						{:else}
							<div class="space-y-2">
								{#each inventory.inputs as device (device.id)}
									<div
										class="flex flex-col gap-2 rounded-lg border border-border/60 p-3 sm:flex-row sm:items-center sm:justify-between"
									>
										<div class="space-y-1">
											<div class="flex items-center gap-2">
												<span class="font-medium">{device.label}</span>
												{#if device.systemDefault}
													<Badge variant="outline">Default</Badge>
												{/if}
												{#if selectedInputId === device.id}
													<Badge>Selected</Badge>
												{/if}
											</div>
											<p class="text-xs text-muted-foreground">{device.id}</p>
										</div>
										<div class="flex flex-wrap items-center gap-2">
											<Button size="sm" variant="ghost" onclick={() => selectInputDevice(device)}
												>Use input</Button
											>
											<Button
												size="sm"
												onclick={() => void startListeningFromDevice(device)}
												disabled={listening && session?.deviceId === device.id}
											>
												{listening && session?.deviceId === device.id
													? 'Listening…'
													: 'Start listening'}
											</Button>
											<Button
												size="sm"
												variant="outline"
												onclick={stopListeningFromDevice}
												disabled={!session?.active && !listening}
											>
												Stop session
											</Button>
										</div>
									</div>
								{/each}
							</div>
						{/if}
					</div>
					<div class="space-y-2">
						<h3 class="text-sm font-medium text-foreground/80">Output devices</h3>
						{#if inventory.outputs.length === 0}
							<p class="text-sm text-muted-foreground">
								No audio outputs were reported. Refresh the inventory or ensure the agent was built
								with audio support (CGO enabled).
							</p>
						{:else}
							<div class="space-y-2">
								{#each inventory.outputs as device (device.id)}
									<div
										class="flex flex-col gap-2 rounded-lg border border-border/60 p-3 sm:flex-row sm:items-center sm:justify-between"
									>
										<div class="space-y-1">
											<div class="flex items-center gap-2">
												<span class="font-medium">{device.label}</span>
												{#if device.systemDefault}
													<Badge variant="outline">Default</Badge>
												{/if}
												{#if selectedOutputId === device.id}
													<Badge>Selected</Badge>
												{/if}
											</div>
											<p class="text-xs text-muted-foreground">{device.id}</p>
										</div>
										<div class="flex flex-wrap items-center gap-2">
											<Button size="sm" variant="ghost" onclick={() => selectOutputDevice(device)}
												>Target output</Button
											>
											<Button
												size="sm"
												onclick={() => void startListeningFromDevice(device)}
												disabled={listening && session?.deviceId === device.id}
											>
												Start listening
											</Button>
											<Button
												size="sm"
												variant="outline"
												onclick={stopListeningFromDevice}
												disabled={!session?.active && !listening}
											>
												Stop session
											</Button>
										</div>
									</div>
								{/each}
							</div>
						{/if}
					</div>
				</div>
			{/if}
		</CardContent>
	</Card>
</div>
