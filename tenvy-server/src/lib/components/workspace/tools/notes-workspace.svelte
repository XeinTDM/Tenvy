<script lang="ts">
	import { browser } from '$app/environment';
	import { onMount } from 'svelte';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Client } from '$lib/data/clients';
	import type { Snippet } from 'svelte';

	const {
		client,
		class: className = '',
		secondary
	} = $props<{
		client: Client;
		class?: string;
		secondary?: Snippet<[{ noteSavePending: boolean }]>;
	}>();

	let noteText = $state(client.notes ?? '');
	let noteTagsInput = $state(client.noteTags?.join(' ') ?? '');
	let noteSavePending = $state(false);
	let noteSaveError = $state<string | null>(null);
	let noteSaveSuccess = $state<string | null>(null);

	const notesFieldId = `client-${client.id}-notes`;

	function parseTags(input: string): string[] {
		return input
			.split(/[,\s]+/)
			.map((tag) => tag.trim())
			.filter(Boolean);
	}

	function normalizeTagsFromResponse(value: unknown): string[] {
		if (!Array.isArray(value)) {
			return [];
		}

		return value.map((tag) => `${tag ?? ''}`.trim()).filter((tag) => tag.length > 0);
	}

	function clearNoteFeedback() {
		noteSaveError = null;
		noteSaveSuccess = null;
	}

	onMount(() => {
		if (!browser) {
			return;
		}

		const controller = new AbortController();

		const loadNote = async () => {
			try {
				const response = await fetch(`/api/agents/${client.id}/notes`, {
					method: 'GET',
					signal: controller.signal
				});

				if (!response.ok) {
					const message = (await response.text())?.trim();
					noteSaveError = message || 'Failed to load notes';
					return;
				}

				let payload: unknown = null;
				try {
					payload = await response.json();
				} catch {
					payload = null;
				}

				const nextNote =
					payload && typeof (payload as Record<string, unknown>).note === 'string'
						? ((payload as { note: string }).note ?? '').trimEnd()
						: '';
				const nextTags = normalizeTagsFromResponse(
					payload && 'tags' in (payload as Record<string, unknown>)
						? (payload as { tags: unknown }).tags
						: []
				);
				const updatedAt =
					payload && 'updatedAt' in (payload as Record<string, unknown>)
						? ((payload as { updatedAt: unknown }).updatedAt ?? null)
						: null;
				const updatedBy =
					payload && 'updatedBy' in (payload as Record<string, unknown>)
						? ((payload as { updatedBy: unknown }).updatedBy ?? null)
						: null;

				noteText = nextNote;
				noteTagsInput = nextTags.join(' ');
				client.notes = nextNote;
				client.noteTags = nextTags;
				client.noteUpdatedAt =
					typeof updatedAt === 'string' || updatedAt === null ? updatedAt : null;
				client.noteUpdatedBy = typeof updatedBy === 'string' ? updatedBy : null;
				noteSaveError = null;
				noteSaveSuccess = null;
			} catch (err) {
				if (err instanceof DOMException && err.name === 'AbortError') {
					return;
				}
				noteSaveError = err instanceof Error ? err.message : 'Failed to load notes';
			}
		};

		void loadNote();

		return () => {
			controller.abort();
		};
	});

	async function handleSubmit(event: SubmitEvent) {
		event.preventDefault();

		noteSaveError = null;
		noteSaveSuccess = null;

		if (!browser) {
			noteSaveError = 'Notes cannot be saved in this environment';
			return;
		}

		const trimmed = noteText.trimEnd();
		const tags = parseTags(noteTagsInput);

		noteSavePending = true;

		try {
			const response = await fetch(`/api/agents/${client.id}/notes`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ note: trimmed, tags })
			});

			if (!response.ok) {
				const message = (await response.text())?.trim();
				noteSaveError = message || 'Failed to save notes';
				return;
			}

			let responseBody: unknown = null;
			try {
				responseBody = await response.json();
			} catch {
				responseBody = null;
			}

			const nextNote =
				responseBody && typeof (responseBody as Record<string, unknown>).note === 'string'
					? ((responseBody as { note: string }).note ?? '').trimEnd()
					: trimmed;
			const nextTags =
				responseBody && 'tags' in (responseBody as Record<string, unknown>)
					? normalizeTagsFromResponse((responseBody as { tags: unknown }).tags)
					: tags;
			const updatedAt =
				responseBody && 'updatedAt' in (responseBody as Record<string, unknown>)
					? ((responseBody as { updatedAt: unknown }).updatedAt ?? null)
					: null;
			const updatedBy =
				responseBody && 'updatedBy' in (responseBody as Record<string, unknown>)
					? ((responseBody as { updatedBy: unknown }).updatedBy ?? null)
					: null;

			noteText = nextNote;
			client.notes = nextNote;
			noteTagsInput = nextTags.join(' ');
			client.noteTags = nextTags;
			client.noteUpdatedAt = typeof updatedAt === 'string' || updatedAt === null ? updatedAt : null;
			client.noteUpdatedBy = typeof updatedBy === 'string' ? updatedBy : null;
			noteSaveSuccess = 'Notes saved';
		} catch (err) {
			noteSaveError = err instanceof Error ? err.message : 'Failed to save notes';
		} finally {
			noteSavePending = false;
		}
	}
</script>

<form class={`flex h-full flex-col ${className}`} onsubmit={handleSubmit}>
	<div class="flex-1 space-y-6 overflow-auto px-6 py-5">
		<div class="grid gap-2">
			<Label for={notesFieldId}>Operational notes</Label>
			<Textarea
				id={notesFieldId}
				class="min-h-32"
				bind:value={noteText}
				on:input={() => clearNoteFeedback()}
				placeholder={`Add context, requirements, or follow-up actions for ${client.codename}.`}
			/>
		</div>
		<div class="grid gap-2">
			<Label for={`${notesFieldId}-tags`}>Quick tags</Label>
			<Input
				id={`${notesFieldId}-tags`}
				bind:value={noteTagsInput}
				on:input={() => clearNoteFeedback()}
				placeholder="intel priority staging"
			/>
		</div>
		{#if noteSaveError}
			<p class="text-sm text-destructive">{noteSaveError}</p>
		{:else if noteSaveSuccess}
			<p class="text-sm text-emerald-600">{noteSaveSuccess}</p>
		{/if}
	</div>
	<div class="flex items-center justify-end gap-2 border-t border-border/70 bg-muted/30 px-6 py-4">
		{@render secondary?.({ noteSavePending })}
		<Button type="submit" disabled={noteSavePending}>
			{#if noteSavePending}
				Saving…
			{:else}
				Save draft
			{/if}
		</Button>
	</div>
</form>
