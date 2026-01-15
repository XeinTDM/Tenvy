<script lang="ts">
	import { browser } from '$app/environment';
	import { onMount } from 'svelte';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import { Card, CardContent, CardFooter } from '$lib/components/ui/card/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import {
		Select,
		SelectContent,
		SelectItem,
		SelectTrigger
	} from '$lib/components/ui/select/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import type { Client } from '$lib/data/clients';
	import type {
		RemoteDesktopInputEvent,
		RemoteDesktopMonitor,
		RemoteDesktopMouseButton,
		RemoteDesktopSessionState,
		RemoteDesktopSettings,
		RemoteDesktopTransport,
		RemoteDesktopHardwarePreference,
		RemoteDesktopTransportDiagnostics,
		RemoteDesktopSettingsPatch
	} from '$lib/types/remote-desktop';
	import SessionMetricsGrid from './remote-desktop/SessionMetricsGrid.svelte';
	import { createInputChannel } from './remote-desktop/input-channel';
	import { encode as encodeMsgpack } from '@msgpack/msgpack';
	import { appendWorkspaceLog, createWorkspaceLogEntry } from '$lib/workspace/utils';
	import type { WorkspaceLogEntry } from '$lib/workspace/types';
	import { createSessionController } from './remote-desktop/session-controller.svelte';
	import { createTransportController } from './remote-desktop/transport-controller.svelte';
	import { createCanvasRenderer } from './remote-desktop/canvas-renderer.svelte';
	import { createAudioController } from './remote-desktop/audio-controller.svelte';
	import { clamp } from './remote-desktop/utils';

	const fallbackMonitors = [
		{ id: 0, label: 'Primary', width: 1280, height: 720 }
	] satisfies RemoteDesktopMonitor[];

	const qualityOptions = [
		{ value: 'auto', label: 'Auto' },
		{ value: 'high', label: 'High' },
		{ value: 'medium', label: 'Medium' },
		{ value: 'low', label: 'Low' }
	] satisfies { value: RemoteDesktopSettings['quality']; label: string }[];

	const encoderOptions = [
		{ value: 'auto', label: 'Auto' },
		{ value: 'hevc', label: 'HEVC (H.265)' },
		{ value: 'avc', label: 'AVC (H.264)' },
		{ value: 'jpeg', label: 'JPEG' }
	] satisfies { value: RemoteDesktopSettings['encoder']; label: string }[];

	const transportOptions = [
		{ value: 'webrtc', label: 'WebRTC (low latency)' },
		{ value: 'http', label: 'HTTP fallback' }
	] satisfies { value: RemoteDesktopTransport; label: string }[];

	const hardwareOptions = [
		{ value: 'auto', label: 'Auto' },
		{ value: 'prefer', label: 'Prefer hardware' },
		{ value: 'avoid', label: 'Avoid hardware' }
	] satisfies { value: RemoteDesktopHardwarePreference; label: string }[];

	let {
		client,
		initialSession = null,
		onLogChange
	} = $props<{
		client: Client;
		initialSession?: RemoteDesktopSessionState | null;
		onLogChange?: (log: WorkspaceLogEntry[]) => void;
	}>();

	let log = $state<WorkspaceLogEntry[]>([]);
	$effect(() => {
		onLogChange?.(log);
	});

	// UI State synced with session
	let quality = $state<RemoteDesktopSettings['quality']>('auto');
	let encoder = $state<RemoteDesktopSettings['encoder']>('auto');
	let transportPreference = $state<RemoteDesktopTransport>('webrtc');
	let hardwarePreference = $state<RemoteDesktopHardwarePreference>('auto');
	let targetBitrateKbps = $state<number | null>(null);
	let mode = $state<RemoteDesktopSettings['mode']>('video');
	let monitor = $state(0);
	let mouseEnabled = $state(false);
	let keyboardEnabled = $state(false);
	
	// Metrics & Diagnostics
	let fps = $state<number | null>(null);
	let bandwidth = $state<number | null>(null);
	let streamWidth = $state<number | null>(null);
	let streamHeight = $state<number | null>(null);
	let latencyMs = $state<number | null>(null);
	let encoderHardware = $state<string | null>(null);
	let monitors = $state<RemoteDesktopMonitor[]>(fallbackMonitors);

	let viewportEl = $state<HTMLDivElement | null>(null);
	let webrtcVideoEl = $state<HTMLVideoElement | null>(null);
	let canvasEl = $state<HTMLCanvasElement | null>(null);
	let viewportFocused = $state(false);
	let pointerCaptured = $state(false);
	let activePointerId: number | null = null;

	const sessionController = createSessionController({
		agentId: client.id,
		initialSession
	});

	const audioController = createAudioController();
	
	const renderer = createCanvasRenderer({
		onMetrics: (m) => {
			if (m.fps !== undefined) fps = m.fps;
			if (m.bandwidthKbps !== undefined) bandwidth = m.bandwidthKbps;
			if (m.width !== undefined) streamWidth = m.width;
			if (m.height !== undefined) streamHeight = m.height;
			if (m.encoderHardware !== undefined) encoderHardware = m.encoderHardware;
			if (m.monitors) monitors = m.monitors;
		},
		computeLatency: (ts) => inputChannel?.computeLatency(ts) ?? null
	});

	const transport = createTransportController({
		agentId: client.id,
		onFrame: (frame) => {
			renderer.enqueueFrame(frame);
			latencyMs = inputChannel?.computeLatency(frame.timestamp) ?? null;
		},
		onMedia: (sessionId, media) => {
			media.forEach(sample => {
				if (sample.kind === 'audio') audioController.handleAudioSample(sample);
			});
		},
		onSessionUpdate: (s) => {
			sessionController.session = s;
		},
		onEnd: (reason) => {
			if (sessionController.session) {
				sessionController.session = { ...sessionController.session, active: false };
			}
			sessionController.infoMessage = reason ?? 'Remote desktop session ended.';
			transport.disconnectStream();
		},
		onError: (msg) => sessionController.errorMessage = msg,
		onInfo: (msg) => sessionController.infoMessage = msg
	});

	// Alises for template
	const session = $derived(sessionController.session);
	const sessionActive = $derived(session?.active ?? false);
	const isStarting = $derived(sessionController.isStarting);
	const isStopping = $derived(sessionController.isStopping);
	const isUpdating = $derived(sessionController.isUpdating);
	const errorMessage = $derived(sessionController.errorMessage);
	const infoMessage = $derived(sessionController.infoMessage);
	const webrtcVideoActive = $derived(transport.webrtcVideoActive);
	const transportDiagnostics = $derived(transport.transportDiagnostics);

	const inputChannel = browser
		? createInputChannel({
				dispatch: async (events) => {
					if (!client || !session?.active || !session?.sessionId) {
						return false;
					}
					const payload = encodeMsgpack({ sessionId: session.sessionId, events });
					const response = await fetch(`/api/agents/${client.id}/remote-desktop/input`, {
						method: 'POST',
						headers: { 'Content-Type': 'application/msgpack' },
						body: payload as BodyInit,
						keepalive: true
					});
					return response.ok;
				}
			})
		: null;

	function handleViewportFocus() { viewportFocused = true; }
	function handleViewportBlur() {
		viewportFocused = false;
		releasePointerCapture();
		releaseAllPressedKeys();
	}

	function handlePointerLeave(event: PointerEvent) {
		releasePointerCapture();
	}

	function releasePointerCapture() {
		if (viewportEl && pointerCaptured && activePointerId !== null) {
			try { viewportEl.releasePointerCapture(activePointerId); } catch {}
		}
		pointerCaptured = false;
		activePointerId = null;
	}

	function handlePointerDown(event: PointerEvent) {
		if (!browser || event.pointerType !== 'mouse' || !mouseEnabled || !sessionActive) return;
		event.preventDefault();
		viewportEl?.focus();
		handlePointerMove(event);
		const button = pointerButtonFromEvent(event.button);
		if (button) {
			queueInput({ type: 'mouse-button', button, pressed: true, monitor, capturedAt: Date.now() });
		}
		if (event.currentTarget instanceof HTMLElement) {
			try {
				event.currentTarget.setPointerCapture(event.pointerId);
				pointerCaptured = true;
				activePointerId = event.pointerId;
			} catch {}
		}
	}

	function handlePointerMove(event: PointerEvent) {
		if (!browser || event.pointerType !== 'mouse' || !mouseEnabled || !sessionActive || !canvasEl) return;
		const rect = canvasEl.getBoundingClientRect();
		if (rect.width <= 0 || rect.height <= 0) return;
		const x = clamp((event.clientX - rect.left) / rect.width, 0, 1);
		const y = clamp((event.clientY - rect.top) / rect.height, 0, 1);
		queueInput({ type: 'mouse-move', x, y, normalized: true, monitor, capturedAt: Date.now() });
	}

	function handlePointerUp(event: PointerEvent) {
		if (!browser || event.pointerType !== 'mouse') return;
		if (!mouseEnabled || !sessionActive) {
			releasePointerCapture();
			return;
		}
		event.preventDefault();
		const button = pointerButtonFromEvent(event.button);
		if (button) {
			queueInput({ type: 'mouse-button', button, pressed: false, monitor, capturedAt: Date.now() });
		}
		if (pointerCaptured && activePointerId === event.pointerId) releasePointerCapture();
	}

	function handleWheel(event: WheelEvent) {
		if (!mouseEnabled || !sessionActive) return;
		event.preventDefault();
		event.stopPropagation();
		queueInput({ type: 'mouse-scroll', deltaX: event.deltaX, deltaY: event.deltaY, deltaMode: event.deltaMode, monitor, capturedAt: Date.now() });
	}

	function handleKeyDown(event: KeyboardEvent) {
		if (!keyboardEnabled || !sessionActive || !viewportFocused) return;
		const keyCode = event.keyCode || event.which;
		event.preventDefault();
		queueInput({
			type: 'key',
			pressed: true,
			keyCode,
			key: event.key,
			code: event.code,
			repeat: event.repeat,
			altKey: event.altKey,
			ctrlKey: event.ctrlKey,
			shiftKey: event.shiftKey,
			metaKey: event.metaKey,
			capturedAt: Date.now()
		});
	}

	function handleKeyUp(event: KeyboardEvent) {
		if (!keyboardEnabled || !sessionActive) return;
		const keyCode = event.keyCode || event.which;
		event.preventDefault();
		queueInput({
			type: 'key',
			pressed: false,
			keyCode,
			key: event.key,
			code: event.code,
			altKey: event.altKey,
			ctrlKey: event.ctrlKey,
			shiftKey: event.shiftKey,
			metaKey: event.metaKey,
			capturedAt: Date.now()
		});
	}

	function releaseAllPressedKeys() {
		// Simplified for now
		inputChannel?.clear();
	}

	function queueInput(event: RemoteDesktopInputEvent) {
		inputChannel?.enqueue(event);
	}

	function pointerButtonFromEvent(button: number): RemoteDesktopMouseButton | null {
		if (button === 0) return 'left';
		if (button === 1) return 'middle';
		if (button === 2) return 'right';
		return null;
	}

	function handleBitrateInput(event: Event) {
		const element = event.currentTarget as HTMLInputElement;
		const parsed = Number.parseInt(element.value, 10);
		targetBitrateKbps = (Number.isNaN(parsed) || parsed <= 0) ? null : parsed;
		if (session?.active && session?.sessionId) {
			void sessionController.updateSession(session.sessionId, { targetBitrateKbps: targetBitrateKbps ?? 0 });
		}
	}

	const qualityLabel = (value: string) => {
		const found = qualityOptions.find((item) => item.value === value);
		return found ? found.label : value;
	};

	const transportLabel = (value: RemoteDesktopTransport) => {
		const found = transportOptions.find((item) => item.value === value);
		return found ? found.label : value;
	};

	const hardwareLabel = (value: RemoteDesktopHardwarePreference) => {
		const found = hardwareOptions.find((item) => item.value === value);
		return found ? found.label : value;
	};

	function formatDiagnosticsSummary(diag: RemoteDesktopTransportDiagnostics | null) {
		if (!diag) return '—';
		const parts: string[] = [];
		if (typeof diag.currentBitrateKbps === 'number') parts.push(`${Math.round(diag.currentBitrateKbps)} kbps`);
		if (typeof diag.rttMs === 'number') parts.push(`${Math.round(diag.rttMs)} ms RTT`);
		if (typeof diag.jitterMs === 'number') parts.push(`${Math.round(diag.jitterMs)} ms jitter`);
		return parts.length === 0 ? '—' : parts.join(' · ');
	}

	const monitorLabel = (id: number) => {
		const found = monitors.find((item: RemoteDesktopMonitor) => item.id === id);
		return found ? `${found.label} · ${found.width}×${found.height}` : `Monitor ${id + 1}`;
	};

	async function updateSession(partial: RemoteDesktopSettingsPatch) {
		if (session?.sessionId) {
			await sessionController.updateSession(session.sessionId, partial);
		}
	}

	$effect(() => {
		const s = session;
		if (!s) {
			quality = 'auto'; encoder = 'auto'; transportPreference = 'webrtc'; hardwarePreference = 'auto';
			targetBitrateKbps = null; mode = 'video'; monitor = 0; mouseEnabled = true; keyboardEnabled = true;
			monitors = fallbackMonitors; fps = null; bandwidth = null;
			return;
		}
		quality = s.settings.quality;
		encoder = s.settings.encoder ?? 'auto';
		mode = s.settings.mode;
		monitor = s.settings.monitor;
		mouseEnabled = s.settings.mouse;
		keyboardEnabled = s.settings.keyboard;
		transportPreference = s.settings.transport ?? 'webrtc';
		hardwarePreference = s.settings.hardware ?? 'auto';
		targetBitrateKbps = (s.settings.targetBitrateKbps ?? 0) > 0 ? s.settings.targetBitrateKbps! : null;
		monitors = s.monitors?.length ? s.monitors : fallbackMonitors;
		if (s.metrics) {
			fps = s.metrics.fps ?? fps;
			bandwidth = s.metrics.bandwidthKbps ?? bandwidth;
		}
	});

	$effect(() => {
		renderer.canvasEl = canvasEl;
	});

	$effect(() => {
		transport.webrtcVideoEl = webrtcVideoEl;
	});

	$effect(() => {
		const active = session?.active;
		const sid = session?.sessionId;
		if (active && sid) {
			transport.connectStream(sid);
			if (transportPreference === 'webrtc') {
				void transport.negotiateWebRTC(sid, { mode }, null);
			}
		} else {
			transport.disconnectStream();
			transport.teardownWebRTC();
			renderer.clear();
			audioController.clear();
		}
	});

	onMount(() => {
		if (!browser) return;
		void sessionController.refreshSession().then(s => {
			if (!s?.active) void startSession();
		});
		return () => {
			transport.disconnectStream();
			transport.teardownWebRTC();
		};
	});

	async function startSession() {
		try {
			const s = await sessionController.startSession({
				quality, monitor, mode, encoder, mouse: mouseEnabled, keyboard: keyboardEnabled
			});
			log = appendWorkspaceLog(log, createWorkspaceLogEntry('Remote desktop started', `Session ${s?.sessionId ?? ''}`, 'complete'));
		} catch {}
	}

	async function stopSession() {
		if (!session?.sessionId) return;
		try {
			await sessionController.stopSession(session.sessionId);
			log = appendWorkspaceLog(log, createWorkspaceLogEntry('Remote desktop paused', `Session ${session.sessionId}`, 'complete'));
		} catch {}
	}
</script>

<svelte:window onkeydown={handleKeyDown} onkeyup={handleKeyUp} />


<Card>
	<CardContent>
		<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
		<div
			tabindex={0}
			bind:this={viewportEl}
			class="relative overflow-hidden rounded-lg border border-border bg-muted/30 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
			role="application"
			aria-label="Remote desktop viewport"
			onfocus={handleViewportFocus}
			onblur={handleViewportBlur}
			onpointerdown={handlePointerDown}
			onpointerup={handlePointerUp}
			onpointermove={handlePointerMove}
			onpointerleave={handlePointerLeave}
			onpointercancel={handlePointerLeave}
			onwheel={handleWheel}
			style="touch-action: none;"
		>
			<video
				bind:this={webrtcVideoEl}
				class="absolute inset-0 h-full w-full object-contain transition-opacity duration-150"
				class:opacity-0={!webrtcVideoActive}
				autoplay
				playsinline
				muted
				controls={false}
				style="pointer-events: none; image-rendering: pixelated;"
			></video>
			<canvas
				bind:this={canvasEl}
				class="block h-full w-full bg-slate-950"
				style="image-rendering: pixelated;"
				class:hidden={webrtcVideoActive}
			></canvas>
			{#if !sessionActive}
				<div
					class="absolute inset-0 flex items-center justify-center text-sm text-muted-foreground"
				>
					Session inactive · start streaming to receive frames
				</div>
			{/if}
		</div>
		<SessionMetricsGrid {fps} {bandwidth} {streamWidth} {streamHeight} {latencyMs} />
		<div class="mt-3 grid gap-2 text-xs text-muted-foreground sm:grid-cols-2">
			<div class="space-y-1">
				<p>
					<span class="font-semibold text-foreground">Transport:</span>
					{session?.negotiatedTransport ? transportLabel(session.negotiatedTransport) : '—'}
					{session?.negotiatedCodec ? ` · ${session.negotiatedCodec.toUpperCase()}` : ''}
				</p>
				<p>
					<span class="font-semibold text-foreground">Hardware encoder:</span>
					{encoderHardware ?? '—'} · {hardwareLabel(hardwarePreference)}
				</p>
			</div>
			<div class="space-y-1">
				<p>
					<span class="font-semibold text-foreground">Target bitrate:</span>
					{targetBitrateKbps ? `${targetBitrateKbps} kbps` : 'Auto'}
				</p>
				<p>
					<span class="font-semibold text-foreground">Observed:</span>
					{formatDiagnosticsSummary(transportDiagnostics)}
				</p>
			</div>
		</div>
		{#if errorMessage}
			<p class="text-sm text-destructive">{errorMessage}</p>
		{/if}
		{#if infoMessage}
			<p class="text-sm text-emerald-500">{infoMessage}</p>
		{/if}
	</CardContent>
	<CardFooter
		class="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground"
	>
		<div class="flex flex-wrap gap-4">
			<div class="w-70">
				<Label class="text-sm font-medium" for="quality-select">Quality</Label>
				<Select
					type="single"
					value={quality}
					onValueChange={(value) => {
						quality = value as RemoteDesktopSettings['quality'];
						if (sessionActive) {
							void updateSession({ quality });
						}
					}}
				>
					<SelectTrigger id="quality-select" class="w-full" disabled={isUpdating && sessionActive}>
						<span class="truncate">{qualityLabel(quality)}</span>
					</SelectTrigger>
					<SelectContent>
						{#each qualityOptions as option (option.value)}
							<SelectItem value={option.value}>{option.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<div class="w-70">
				<Label class="text-sm font-medium" for="transport-select">Transport</Label>
				<Select
					type="single"
					value={transportPreference}
					onValueChange={(value) => {
						transportPreference = value as RemoteDesktopTransport;
						if (sessionActive) {
							void updateSession({ transport: transportPreference });
						}
					}}
				>
					<SelectTrigger
						id="transport-select"
						class="w-full"
						disabled={isUpdating && sessionActive}
					>
						<span class="truncate">{transportLabel(transportPreference)}</span>
					</SelectTrigger>
					<SelectContent>
						{#each transportOptions as option (option.value)}
							<SelectItem value={option.value}>{option.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<div class="w-70">
				<Label class="text-sm font-medium" for="encoder-select">Encoder</Label>
				<Select
					type="single"
					value={encoder}
					onValueChange={(value) => {
						encoder = value as RemoteDesktopSettings['encoder'];
						if (sessionActive) {
							void updateSession({ encoder });
						}
					}}
				>
					<SelectTrigger id="encoder-select" class="w-full" disabled={isUpdating && sessionActive}>
						<span class="truncate"
							>{encoderOptions.find((item) => item.value === encoder)?.label ?? encoder}</span
						>
					</SelectTrigger>
					<SelectContent>
						{#each encoderOptions as option (option.value)}
							<SelectItem value={option.value}>{option.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<div class="w-70">
				<Label class="text-sm font-medium" for="hardware-select">Hardware</Label>
				<Select
					type="single"
					value={hardwarePreference}
					onValueChange={(value) => {
						hardwarePreference = value as RemoteDesktopHardwarePreference;
						if (sessionActive) {
							void updateSession({ hardware: hardwarePreference });
						}
					}}
				>
					<SelectTrigger id="hardware-select" class="w-full" disabled={isUpdating && sessionActive}>
						<span class="truncate">{hardwareLabel(hardwarePreference)}</span>
					</SelectTrigger>
					<SelectContent>
						{#each hardwareOptions as option (option.value)}
							<SelectItem value={option.value}>{option.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<div class="w-70">
				<Label class="text-sm font-medium" for="monitor-select">Monitor</Label>
				<Select
					type="single"
					value={monitor.toString()}
					onValueChange={(value) => {
						const next = Number.parseInt(value, 10);
						monitor = Number.isNaN(next) ? 0 : next;
						if (sessionActive) {
							void updateSession({ monitor });
						}
					}}
				>
					<SelectTrigger id="monitor-select" class="w-full" disabled={isUpdating && sessionActive}>
						<span class="truncate">{monitorLabel(monitor)}</span>
					</SelectTrigger>
					<SelectContent>
						{#each monitors as item (item.id)}
							<SelectItem value={item.id.toString()}>
								Monitor {item.id + 1} · {item.width}×{item.height}
							</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<div class="w-56">
				<Label class="text-sm font-medium" for="bitrate-input">Target bitrate (kbps)</Label>
				<Input
					id="bitrate-input"
					type="number"
					min="0"
					step="100"
					placeholder="Auto"
					value={targetBitrateKbps ?? ''}
					disabled={!sessionActive || isUpdating}
					oninput={handleBitrateInput}
				/>
			</div>
			<div class="flex items-center gap-2">
				<p class="text-sm font-medium">Mouse control</p>
				<Switch
					bind:checked={mouseEnabled}
					disabled={!sessionActive || isUpdating}
					aria-label="Toggle mouse control"
				/>
			</div>

			<div class="flex items-center gap-2">
				<p class="text-sm font-medium">Keyboard control</p>
				<Switch
					bind:checked={keyboardEnabled}
					disabled={!sessionActive || isUpdating}
					aria-label="Toggle keyboard control"
				/>
			</div>
		</div>
		<div class="flex gap-2">
			{#if sessionActive}
				<Button variant="destructive" disabled={isStopping} onclick={() => stopSession()}>
					{isStopping ? 'Pausing…' : 'Pause session'}
				</Button>
			{:else}
				<Button disabled={isStarting} onclick={startSession}>
					{isStarting ? 'Starting…' : 'Start session'}
				</Button>
			{/if}
		</div>
	</CardFooter>
</Card>
