<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { CircleQuestionMark } from '@lucide/svelte';
	import { formatPortSummary, parsePortInput } from '$lib/utils/rat-port-preferences.js';

	let {
		open = $bindable(),
		selectedPorts = [],
		rememberPorts = false,
		onSave,
		onClear
	} = $props<{
		open: boolean;
		selectedPorts: number[];
		rememberPorts: boolean;
		onSave: (ports: number[], remember: boolean) => void;
		onClear: () => void;
	}>();

	let portInputValue = $state('');
	let portDialogRemember = $state(false);
	let portDialogError = $state<string | null>(null);

	// Reset form when dialog opens
	$effect(() => {
		if (open) {
			portInputValue = formatPortSummary(selectedPorts);
			portDialogRemember = rememberPorts;
			portDialogError = null;
		}
	});

	function handlePortSubmit(e: Event) {
		e.preventDefault();
		const trimmed = portInputValue.trim();
		const result = parsePortInput(trimmed);

		if (!result.ok) {
			portDialogError = result.error;
			return;
		}

		onSave(result.ports, portDialogRemember);
		open = false;
	}

	function handleClear() {
		onClear();
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title class="flex items-center gap-2">
				Configure listening ports
				<Tooltip.Provider>
					<Tooltip.Root>
						<Tooltip.Trigger class="cursor-help">
							<CircleQuestionMark class="h-4 w-4 text-muted-foreground" />
						</Tooltip.Trigger>
						<Tooltip.Content side="top" align="center" class="max-w-xs text-xs">
							Choose the ports to listen on once you are signed in.
						</Tooltip.Content>
					</Tooltip.Root>
				</Tooltip.Provider>
			</Dialog.Title>
		</Dialog.Header>
		<form class="space-y-6" onsubmit={handlePortSubmit}>
			<div class="space-y-2">
				<Label for="port-input">Listening ports</Label>
				<Input
					id="port-input"
					placeholder="4444, 8080"
					bind:value={portInputValue}
					inputmode="numeric"
					autocomplete="off"
					spellcheck={false}
					aria-invalid={Boolean(portDialogError)}
					aria-describedby={`port-input-hint${portDialogError ? ' port-input-error' : ''}`}
				/>
				<p id="port-input-hint" class="text-xs text-muted-foreground">
					Separate multiple ports with commas or spaces. Valid range: 1 to 65,535.
				</p>
			</div>
			<div class="flex items-start gap-3">
				<Checkbox
					id="remember-ports"
					bind:checked={portDialogRemember}
					aria-describedby="remember-ports-hint"
				/>
				<div class="grid gap-1">
					<Label for="remember-ports" class="leading-none">Remember selected ports</Label>
					<p id="remember-ports-hint" class="text-xs text-muted-foreground">
						Store this preference locally and reuse it for future sessions.
					</p>
				</div>
			</div>
			{#if portDialogError}
				<p id="port-input-error" class="text-sm text-destructive">{portDialogError}</p>
			{/if}

			<Dialog.Footer>
				{#if selectedPorts.length > 0}
					<Button
						type="button"
						variant="ghost"
						class="justify-start text-destructive hover:text-destructive focus-visible:ring-destructive/20"
						onclick={handleClear}
					>
						Clear saved ports
					</Button>
				{/if}
				<Button type="submit">Save ports</Button>
				{#if selectedPorts.length > 0}
					<Dialog.Close>
						{#snippet child({ props })}
							<Button {...props} type="button" variant="outline">Cancel</Button>
						{/snippet}
					</Dialog.Close>
				{/if}
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>
