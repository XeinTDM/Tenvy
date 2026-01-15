<script lang="ts">
	import { onDestroy } from 'svelte';
	import { get } from 'svelte/store';
	import { Button } from '$lib/components/ui/button/index.js';
	import {
		Card,
		CardContent,
		CardDescription,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import {
		Select,
		SelectContent,
		SelectItem,
		SelectTrigger
	} from '$lib/components/ui/select/index.js';
	import type { Client } from '$lib/data/clients';
	import type {
		AppVncApplicationDescriptor,
		AppVncPlatform,
		AppVncInputEvent,
		AppVncSessionSettings,
		AppVncSessionState
	} from '$lib/types/app-vnc';
	import { createAppVncSessionController } from '$lib/stores/app-vnc-session';

	const qualityOptions: { value: AppVncSessionSettings['quality']; label: string }[] = [
		{ value: 'lossless', label: 'Lossless' },
		{ value: 'balanced', label: 'Balanced' },
		{ value: 'bandwidth', label: 'Bandwidth saver' }
	];

	let { data } = $props<{
		data: {
			session: AppVncSessionState | null;
			client: Client;
			applications: AppVncApplicationDescriptor[];
		};
	}>();

	const client = $derived.by<Client>(() => data.client);
	const applications = $derived.by<AppVncApplicationDescriptor[]>(() => data.applications);
	const {
		session: sessionStore,
		frameUrl,
		frameWidth,
		frameHeight,
		lastHeartbeat,
		isStarting,
		isStopping,
		isUpdating,
		errorMessage,
		infoMessage,
		startSession: startSessionInternal,
		updateSession: updateSessionInternal,
		stopSession: stopSessionInternal,
		enqueueEvent,
		dispose
	} = createAppVncSessionController({
		clientId: data.client.id,
		initialSession: data.session ?? null
	});
	let quality = $state<AppVncSessionSettings['quality']>(
		data.session?.settings.quality ?? 'balanced'
	);
	let monitor = $state(data.session?.settings.monitor ?? 'Primary');
	let captureCursor = $state(data.session?.settings.captureCursor ?? true);
	let clipboardSync = $state(data.session?.settings.clipboardSync ?? false);
	let blockLocalInput = $state(data.session?.settings.blockLocalInput ?? false);
	let heartbeatInterval = $state<number | string>(data.session?.settings.heartbeatInterval ?? 30);
	let appId = $state(data.session?.settings.appId ?? '');
	let windowTitle = $state(data.session?.settings.windowTitle ?? '');
	let viewportEl: HTMLDivElement | null = null;
	let pointerActive = false;
	let activePointerId: number | null = null;
	const platformLabels: Record<AppVncPlatform, string> = {
		windows: 'Windows',
		linux: 'Linux',
		macos: 'macOS'
	};
	const normalizedAppId = $derived.by<string>(() => appId.trim());
	const selectedApp = $derived.by<AppVncApplicationDescriptor | null>(() => {
		const trimmed = normalizedAppId;
		return applications.find((app: AppVncApplicationDescriptor) => app.id === trimmed) ?? null;
	});
	const appSelectionLabel = $derived.by<string>(() => {
		if (selectedApp) {
			return selectedApp.name;
		}
		return normalizedAppId ? `Custom · ${normalizedAppId}` : 'Manual selection';
	});

	function formatPlatforms(platforms?: AppVncApplicationDescriptor['platforms']): string {
		if (!platforms || platforms.length === 0) {
			return '';
		}
		return platforms.map((platform) => platformLabels[platform] ?? platform).join(', ');
	}

	function handleAppSelection(value: string) {
		appId = value.trim();
	}

	function resolveHeartbeatInterval(): number {
		const value = heartbeatInterval;
		if (typeof value === 'number') {
			return value;
		}
		const parsed = Number.parseInt(value, 10);
		if (Number.isFinite(parsed)) {
			return parsed;
		}
		return 30;
	}

	function buildSessionSettings(): AppVncSessionSettings {
		const heartbeat = resolveHeartbeatInterval();
		heartbeatInterval = heartbeat;
		const trimmedAppId = normalizedAppId;
		const trimmedWindowTitle = windowTitle.trim();
		return {
			monitor,
			quality,
			captureCursor,
			clipboardSync,
			blockLocalInput,
			heartbeatInterval: heartbeat,
			appId: trimmedAppId || undefined,
			windowTitle: trimmedWindowTitle || undefined
		} satisfies AppVncSessionSettings;
	}

	async function handleStartSession() {
		await startSessionInternal(buildSessionSettings());
	}

	async function handleUpdateSessionSettings() {
		await updateSessionInternal(buildSessionSettings());
	}

	async function handleStopSession() {
		await stopSessionInternal();
	}

	function pointerPosition(event: PointerEvent) {
		const element = viewportEl;
		if (!element) {
			return null;
		}
		const rect = element.getBoundingClientRect();
		if (rect.width <= 0 || rect.height <= 0) {
			return null;
		}
		const x = (event.clientX - rect.left) / rect.width;
		const y = (event.clientY - rect.top) / rect.height;
		return {
			x: Math.min(Math.max(x, 0), 1),
			y: Math.min(Math.max(y, 0), 1)
		};
	}

	function handlePointerMove(event: PointerEvent) {
		const current = get(sessionStore);
		if (!current?.active || !pointerActive) {
			return;
		}
		const position = pointerPosition(event);
		if (!position) {
			return;
		}
		enqueueEvent({
			type: 'pointer-move',
			capturedAt: Date.now(),
			x: position.x,
			y: position.y,
			normalized: true
		} satisfies AppVncInputEvent);
	}

	function handlePointerDown(event: PointerEvent) {
		const current = get(sessionStore);
		if (!current?.active) {
			return;
		}
		viewportEl?.focus();
		pointerActive = true;
		activePointerId = event.pointerId;
		const target = event.currentTarget as HTMLElement | null;
		try {
			target?.setPointerCapture?.(event.pointerId);
		} catch {
			// ignore
		}
		const position = pointerPosition(event);
		if (position) {
			enqueueEvent({
				type: 'pointer-move',
				capturedAt: Date.now(),
				x: position.x,
				y: position.y,
				normalized: true
			} satisfies AppVncInputEvent);
		}
		enqueueEvent({
			type: 'pointer-button',
			capturedAt: Date.now(),
			button: event.button === 2 ? 'right' : event.button === 1 ? 'middle' : 'left',
			pressed: true
		} satisfies AppVncInputEvent);
	}

	function handlePointerUp(event: PointerEvent) {
		if (pointerActive && activePointerId === event.pointerId) {
			enqueueEvent({
				type: 'pointer-button',
				capturedAt: Date.now(),
				button: event.button === 2 ? 'right' : event.button === 1 ? 'middle' : 'left',
				pressed: false
			} satisfies AppVncInputEvent);
			pointerActive = false;
			activePointerId = null;
			const target = event.currentTarget as HTMLElement | null;
			try {
				target?.releasePointerCapture?.(event.pointerId);
			} catch {
				// ignore
			}
		}
	}

	function handlePointerCancel(event: PointerEvent) {
		if (!pointerActive) {
			return;
		}
		pointerActive = false;
		activePointerId = null;
		const target = event.currentTarget as HTMLElement | null;
		try {
			target?.releasePointerCapture?.(event.pointerId);
		} catch {
			// ignore
		}
	}

	function handleWheel(event: WheelEvent) {
		const current = get(sessionStore);
		if (!current?.active) {
			return;
		}
		event.preventDefault();
		enqueueEvent({
			type: 'pointer-scroll',
			capturedAt: Date.now(),
			deltaX: event.deltaX,
			deltaY: event.deltaY,
			deltaMode: event.deltaMode
		} satisfies AppVncInputEvent);
	}

	function handleKey(event: KeyboardEvent, pressed: boolean) {
		const current = get(sessionStore);
		if (!current?.active) {
			return;
		}
		event.preventDefault();
		enqueueEvent({
			type: 'key',
			capturedAt: Date.now(),
			pressed,
			key: event.key,
			code: event.code,
			keyCode: event.keyCode,
			repeat: event.repeat,
			altKey: event.altKey,
			ctrlKey: event.ctrlKey,
			shiftKey: event.shiftKey,
			metaKey: event.metaKey
		} satisfies AppVncInputEvent);
	}

	$effect(() => {
		const current = $sessionStore;
		if (current && current.active) {
			quality = current.settings.quality;
			monitor = current.settings.monitor;
			captureCursor = current.settings.captureCursor;
			clipboardSync = current.settings.clipboardSync;
			blockLocalInput = current.settings.blockLocalInput;
			heartbeatInterval = current.settings.heartbeatInterval;
			appId = current.settings.appId?.trim() ?? '';
			windowTitle = current.settings.windowTitle?.trim() ?? '';
		}
	});

	onDestroy(() => {
		dispose();
	});
</script>

<div class="space-y-6">
	<Card>
		<CardHeader>
			<CardTitle class="text-base">Session parameters</CardTitle>
			<CardDescription>
				Configure the isolated App VNC environment and manage lifecycle actions.
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-6">
			<div class="grid gap-4 md:grid-cols-2">
				<div class="grid gap-4">
					<div class="grid gap-2">
						<Label for="avnc-application">Application profile</Label>
						<Select
							type="single"
							value={selectedApp ? selectedApp.id : ''}
							onValueChange={handleAppSelection}
						>
							<SelectTrigger id="avnc-application" class="w-full">
								<span class="truncate">{appSelectionLabel}</span>
							</SelectTrigger>
							<SelectContent>
								<SelectItem value="">
									<span class="flex flex-col gap-0.5">
										<span class="font-medium">Manual selection</span>
										<span class="text-xs text-muted-foreground"
											>Provide a custom identifier below</span
										>
									</span>
								</SelectItem>
								{#each applications as application (application.id)}
									<SelectItem value={application.id}>
										<span class="flex flex-col gap-0.5">
											<span class="font-medium">{application.name}</span>
											<span class="text-xs text-muted-foreground">{application.summary}</span>
										</span>
									</SelectItem>
								{/each}
							</SelectContent>
						</Select>
						{#if selectedApp}
							<p class="text-xs text-muted-foreground">
								{selectedApp.summary}
								{#if selectedApp.platforms?.length}
									· Supports {formatPlatforms(selectedApp.platforms)}
								{/if}
								{#if selectedApp.windowTitleHint}
									· Window title hint: “{selectedApp.windowTitleHint}”
								{/if}
							</p>
						{:else if normalizedAppId}
							<p class="text-xs text-muted-foreground">
								Custom profile targeting “{normalizedAppId}”. Ensure the agent recognises this
								identifier.
							</p>
						{:else}
							<p class="text-xs text-muted-foreground">
								Choose one of {applications.length} built-in profiles or stay in manual mode to specify
								your own target.
							</p>
						{/if}
					</div>
					<div class="grid gap-2">
						<Label for="avnc-app-id">Custom app identifier</Label>
						<Input
							id="avnc-app-id"
							placeholder="Override or provide your own identifier"
							bind:value={appId}
						/>
						<p class="text-xs text-muted-foreground">
							Applied value is forwarded directly to the agent; leave blank to rely on the selected
							profile.
						</p>
					</div>
				</div>
				<div class="grid gap-2">
					<Label for="avnc-quality">Encoding profile</Label>
					<Select
						type="single"
						value={quality}
						onValueChange={(value) => (quality = value as typeof quality)}
					>
						<SelectTrigger id="avnc-quality" class="w-full">
							<span class="truncate"
								>{qualityOptions.find((q) => q.value === quality)?.label ?? quality}</span
							>
						</SelectTrigger>
						<SelectContent>
							{#each qualityOptions as option (option.value)}
								<SelectItem value={option.value}>{option.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</div>
			</div>

			<div class="grid gap-4 md:grid-cols-2">
				<div class="grid gap-2">
					<Label for="avnc-monitor">Surface label</Label>
					<Input id="avnc-monitor" placeholder="Hidden surface label" bind:value={monitor} />
					<p class="text-xs text-muted-foreground">
						Provide a stable identifier for the virtualised application surface.
					</p>
				</div>
				<div class="grid gap-2">
					<Label for="avnc-window-title">Window title hint</Label>
					<Input
						id="avnc-window-title"
						placeholder="Optional window title"
						bind:value={windowTitle}
					/>
					<p class="text-xs text-muted-foreground">
						Provide optional hints for the agent's window matcher
						{#if selectedApp?.windowTitleHint}
							— suggested: “{selectedApp.windowTitleHint}”.
						{/if}
					</p>
				</div>
			</div>

			<div class="grid gap-4 md:grid-cols-3">
				<label
					class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
				>
					<div>
						<p class="text-sm font-medium text-foreground">Mirror cursor</p>
						<p class="text-xs text-muted-foreground">Display remote cursor state</p>
					</div>
					<Switch bind:checked={captureCursor} />
				</label>
				<label
					class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
				>
					<div>
						<p class="text-sm font-medium text-foreground">Clipboard tunnel</p>
						<p class="text-xs text-muted-foreground">Enable clipboard mirroring</p>
					</div>
					<Switch bind:checked={clipboardSync} />
				</label>
				<label
					class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 p-3"
				>
					<div>
						<p class="text-sm font-medium text-foreground">Lock local input</p>
						<p class="text-xs text-muted-foreground">Block physical keyboard and mouse locally</p>
					</div>
					<Switch bind:checked={blockLocalInput} />
				</label>
			</div>

			<div class="grid gap-2 md:w-1/3">
				<Label for="avnc-heartbeat">Heartbeat interval (seconds)</Label>
				<Input id="avnc-heartbeat" type="number" min={10} step={5} bind:value={heartbeatInterval} />
				<p class="text-xs text-muted-foreground">
					Controls how often the agent renews the isolated session lease.
				</p>
			</div>

			<div class="flex flex-wrap gap-3">
				{#if $sessionStore?.active}
					<Button type="button" onclick={handleUpdateSessionSettings} disabled={$isUpdating}
						>{$isUpdating ? 'Updating…' : 'Update session'}</Button
					>
					<Button type="button" variant="outline" onclick={handleStopSession} disabled={$isStopping}
						>{$isStopping ? 'Stopping…' : 'Stop session'}</Button
					>
				{:else}
					<Button type="button" onclick={handleStartSession} disabled={$isStarting}
						>{$isStarting ? 'Starting…' : 'Start session'}</Button
					>
				{/if}
			</div>

			{#if $errorMessage}
				<p class="text-sm text-destructive">{$errorMessage}</p>
			{/if}
			{#if $infoMessage}
				<p class="text-sm text-emerald-500">{$infoMessage}</p>
			{/if}
		</CardContent>
	</Card>

	<Card>
		<CardHeader>
			<CardTitle class="text-base">Live application surface</CardTitle>
			<CardDescription>
				A headless, single-window workspace rendered via the covert VNC transport.
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-4">
			<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
			<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
			<div
				class="relative flex h-[420px] w-full items-center justify-center overflow-hidden rounded-lg border bg-black"
				role="application"
				tabindex="0"
				bind:this={viewportEl}
				data-testid="app-vnc-viewport"
				onpointerdown={handlePointerDown}
				onpointermove={handlePointerMove}
				onpointerup={handlePointerUp}
				onpointercancel={handlePointerCancel}
				onwheel={handleWheel}
				onkeydown={(event) => handleKey(event, true)}
				onkeyup={(event) => handleKey(event, false)}
			>
				{#if $frameUrl}
					<img
						src={$frameUrl}
						alt="App VNC frame"
						class="max-h-full max-w-full select-none"
						draggable={false}
					/>
				{:else}
					<p class="text-sm text-muted-foreground">No frame data available yet.</p>
				{/if}
			</div>
			<div class="grid gap-2 text-xs text-muted-foreground sm:grid-cols-2">
				<div>
					<span class="font-medium text-foreground">Session</span>
					<span class="ml-2">{$sessionStore?.active ? 'Active' : 'Idle'}</span>
				</div>
				<div>
					<span class="font-medium text-foreground">Frame size</span>
					<span class="ml-2">
						{#if $frameWidth && $frameHeight}
							{$frameWidth}×{$frameHeight}
						{:else}
							—
						{/if}
					</span>
				</div>
				<div>
					<span class="font-medium text-foreground">Last heartbeat</span>
					<span class="ml-2">{$lastHeartbeat ?? '—'}</span>
				</div>
				<div>
					<span class="font-medium text-foreground">Session ID</span>
					<span class="ml-2 truncate">{$sessionStore?.sessionId ?? '—'}</span>
				</div>
			</div>
		</CardContent>
	</Card>
</div>
