<script lang="ts">
	import {
		Dialog as DialogRoot,
		DialogContent,
		DialogDescription,
		DialogFooter,
		DialogHeader,
		DialogTitle
	} from '$lib/components/ui/dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Alert, AlertDescription } from '$lib/components/ui/alert/index.js';
	import X from '@lucide/svelte/icons/x';
	import type { AgentSnapshot } from '../../../../../../shared/types/agent';

	const MAX_TAGS = 16;
	const MAX_TAG_LENGTH = 32;
	const TAG_PATTERN = /^[\p{L}\p{N}_\-\s]+$/u;

	let {
		open = false,
		agent = null,
		availableTags = [],
		pending = false,
		error = null,
		onClose,
		onSubmit
	} = $props<{
		open?: boolean;
		agent?: AgentSnapshot | null;
		availableTags?: string[];
		pending?: boolean;
		error?: string | null;
		onClose?: () => void;
		onSubmit?: (detail: { tags: string[] }) => void;
	}>();

	let draftTags = $state<string[]>([]);
	let tagInput = $state('');
	let inputError = $state<string | null>(null);

	$effect(() => {
		if (!open) {
			tagInput = '';
			inputError = null;
			return;
		}

		if (agent) {
			draftTags = [...(agent.metadata.tags ?? [])];
		} else {
			draftTags = [];
		}
	});

	function closeDialog() {
		if (pending) {
			return;
		}
		onClose?.();
	}

	function normalizeTag(value: string): string | null {
		if (typeof value !== 'string') {
			return null;
		}

		const trimmed = value.trim();
		if (trimmed.length === 0) {
			inputError = 'Tag cannot be empty.';
			return null;
		}

		if (trimmed.length > MAX_TAG_LENGTH) {
			inputError = `Tag cannot exceed ${MAX_TAG_LENGTH} characters.`;
			return null;
		}

		if (!TAG_PATTERN.test(trimmed)) {
			inputError = 'Tags may only include letters, numbers, spaces, hyphens, or underscores.';
			return null;
		}

		return trimmed;
	}

	function addTag(tag?: string) {
		if (pending) {
			return;
		}

		const normalized = normalizeTag(tag ?? tagInput);
		if (!normalized) {
			return;
		}

		if (draftTags.length >= MAX_TAGS) {
			inputError = `You can add up to ${MAX_TAGS} tags.`;
			return;
		}

		const duplicate = draftTags.some(
			(existing) => existing.toLowerCase() === normalized.toLowerCase()
		);
		if (duplicate) {
			inputError = 'Tag already added.';
			return;
		}

		draftTags = [...draftTags, normalized];
		tagInput = '';
		inputError = null;
	}

	function removeTag(tag: string) {
		if (pending) {
			return;
		}
		draftTags = draftTags.filter((value) => value !== tag);
		inputError = null;
	}

	function handleInputKeydown(event: KeyboardEvent) {
		if (event.key === 'Enter') {
			event.preventDefault();
			addTag();
		}
	}

	function handleSubmit() {
		if (pending) {
			return;
		}
		inputError = null;
		onSubmit?.({ tags: draftTags });
	}

	const filteredSuggestions = $derived(
		availableTags
			.filter(
				(tag: string) =>
					!draftTags.some((existing) => existing.toLowerCase() === tag.toLowerCase()) &&
					tag.toLowerCase().includes(tagInput.trim().toLowerCase())
			)
			.slice(0, 12)
	);
</script>

<DialogRoot {open} onOpenChange={(value: boolean) => (!value ? closeDialog() : null)}>
	<DialogContent class="sm:max-w-lg">
		<DialogHeader>
			<DialogTitle>Manage tags</DialogTitle>
			<DialogDescription>
				Apply descriptive labels to the selected client. Tags are shared across the controller UI.
			</DialogDescription>
		</DialogHeader>

		<div class="space-y-4">
			{#if draftTags.length > 0}
				<div class="flex flex-wrap gap-2">
					{#each draftTags as tag (tag)}
						<Badge variant="secondary" class="flex items-center gap-1">
							<span>{tag}</span>
							<button
								type="button"
								class="inline-flex h-4 w-4 cursor-pointer items-center justify-center rounded-full text-muted-foreground transition hover:bg-muted hover:text-foreground"
								onclick={() => removeTag(tag)}
								aria-label={`Remove ${tag}`}
								disabled={pending}
							>
								<X class="h-3 w-3" />
							</button>
						</Badge>
					{/each}
				</div>
			{:else}
				<p class="text-sm text-muted-foreground">
					No tags assigned yet. Add one below to get started.
				</p>
			{/if}

			<div class="space-y-2">
				<label class="flex flex-col gap-2 text-sm font-medium">
					Add tag
					<div class="flex flex-col gap-2 sm:flex-row">
						<Input
							placeholder="For example: priority"
							bind:value={tagInput}
							onkeydown={handleInputKeydown}
							disabled={pending}
							autocomplete="off"
						/>
						<Button type="button" onclick={() => addTag()} disabled={pending}>Add</Button>
					</div>
				</label>
				{#if inputError}
					<p class="text-sm text-destructive">{inputError}</p>
				{/if}
			</div>

			{#if filteredSuggestions.length > 0}
				<div class="space-y-2">
					<p class="text-xs tracking-wide text-muted-foreground uppercase">Suggestions</p>
					<div class="flex flex-wrap gap-2">
						{#each filteredSuggestions as suggestion (suggestion)}
							<Button
								type="button"
								variant="outline"
								class="px-2 py-1 text-xs"
								onclick={() => addTag(suggestion)}
								disabled={pending}
							>
								+ {suggestion}
							</Button>
						{/each}
					</div>
				</div>
			{/if}

			{#if error}
				<Alert variant="destructive">
					<AlertDescription>{error}</AlertDescription>
				</Alert>
			{/if}
		</div>

		<DialogFooter class="mt-4 gap-2">
			<Button type="button" variant="ghost" onclick={closeDialog} disabled={pending}>Cancel</Button>
			<Button type="button" onclick={handleSubmit} disabled={pending}>
				{pending ? 'Saving…' : 'Save tags'}
			</Button>
		</DialogFooter>
	</DialogContent>
</DialogRoot>
