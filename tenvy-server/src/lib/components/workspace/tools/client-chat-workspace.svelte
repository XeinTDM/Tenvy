<script lang="ts">
	import { createEventDispatcher, onDestroy, onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import {
		Card,
		CardContent,
		CardDescription,
		CardFooter,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { getClientTool } from '$lib/data/client-tools';
	import type { Client } from '$lib/data/clients';
	import {
		appendWorkspaceLog,
		createWorkspaceLogEntry,
		formatWorkspaceTimestamp
	} from '$lib/workspace/utils';
	import type { WorkspaceLogEntry } from '$lib/workspace/types';
	import type {
		ClientChatMessage,
		ClientChatMessageResponse,
		ClientChatSessionState,
		ClientChatStateResponse
	} from '$lib/types/client-chat';

	const { client, onLogChange } = $props<{
		client: Client;
		onLogChange?: (log: WorkspaceLogEntry[]) => void;
	}>();

	const tool = getClientTool('client-chat');
	void tool;

	let session = $state<ClientChatSessionState | null>(null);
	let messages = $state<ClientChatMessage[]>([]);
	let draft = $state('');
	let log = $state<WorkspaceLogEntry[]>([]);
	let dispatching = $state(false);
	let loadError = $state<string | null>(null);
	let loadingSession = false;
	let activeFetch: AbortController | null = null;
	let pollTimer: ReturnType<typeof setInterval> | null = null;

	function resolveAlias(message: ClientChatMessage): string {
		if (message.alias?.trim()) {
			return message.alias.trim();
		}
		if (message.sender === 'operator') {
			return session?.operatorAlias ?? 'Operator';
		}
		return session?.clientAlias ?? client.codename;
	}

	function updateLogEntry(id: string, updates: Partial<WorkspaceLogEntry>) {
		log = log.map((entry) => (entry.id === id ? { ...entry, ...updates } : entry));
	}

	function recordDraft() {
		const trimmed = draft.trim();
		if (!trimmed) {
			return;
		}
		log = appendWorkspaceLog(log, createWorkspaceLogEntry('Chat message staged', trimmed, 'draft'));
		loadError = null;
		draft = '';
	}

	function recordFailure(message: string) {
		log = appendWorkspaceLog(
			log,
			createWorkspaceLogEntry('Chat message failed', message, 'failed')
		);
		loadError = message;
	}

	async function refreshSession(signal?: AbortSignal) {
		if (loadingSession) {
			return;
		}

		const controller = signal ? null : new AbortController();
		if (!signal) {
			if (activeFetch) {
				activeFetch.abort();
			}
			activeFetch = controller;
		}

		loadingSession = true;

		try {
			const response = await fetch(`/api/agents/${client.id}/chat`, {
				method: 'GET',
				signal: signal ?? controller?.signal
			});
			if (!response.ok) {
				const detail = (await response.text())?.trim() || 'Failed to load chat session';
				throw new Error(detail);
			}
			const data = (await response.json()) as ClientChatStateResponse;
			session = data.session;
			messages = data.session?.messages ?? [];
			loadError = null;
		} catch (err) {
			if (err instanceof DOMException && err.name === 'AbortError') {
				return;
			}
			const message = err instanceof Error ? err.message : 'Failed to load chat session';
			loadError = message;
		} finally {
			loadingSession = false;
			if (!signal && activeFetch === controller) {
				activeFetch = null;
			}
		}
	}

	async function sendMessage() {
		if (dispatching) {
			return;
		}

		const trimmed = draft.trim();
		if (!trimmed) {
			recordFailure('Message body is required');
			return;
		}

		loadError = null;
		draft = '';

		const logEntry = createWorkspaceLogEntry('Chat message dispatched', trimmed, 'queued');
		log = appendWorkspaceLog(log, logEntry);

		dispatching = true;

		try {
			const response = await fetch(`/api/agents/${client.id}/chat`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					action: 'send-message',
					message: { body: trimmed }
				})
			});

			if (!response.ok) {
				const message = (await response.text())?.trim() || 'Failed to dispatch chat message';
				updateLogEntry(logEntry.id, {
					status: 'failed',
					detail: message
				});
				loadError = message;
				return;
			}

			const data = (await response.json()) as ClientChatMessageResponse;
			session = data.session;
			messages = data.session?.messages ?? [];
			updateLogEntry(logEntry.id, {
				status: 'complete',
				detail: data.accepted ? 'Message accepted by chat session' : 'Message queued for delivery'
			});
			loadError = null;
		} catch (err) {
			const message = err instanceof Error ? err.message : 'Failed to dispatch chat message';
			updateLogEntry(logEntry.id, {
				status: 'failed',
				detail: message
			});
			loadError = message;
		} finally {
			dispatching = false;
		}
	}

	$effect(() => {
		onLogChange?.(log);
	});

	onMount(() => {
		const controller = new AbortController();
		void refreshSession(controller.signal);
		pollTimer = setInterval(() => {
			void refreshSession();
		}, 5000);
		return () => {
			controller.abort();
		};
	});

	onDestroy(() => {
		if (pollTimer) {
			clearInterval(pollTimer);
			pollTimer = null;
		}
		if (activeFetch) {
			activeFetch.abort();
			activeFetch = null;
		}
	});
</script>

<div class="space-y-6">
	{#if loadError}
		<p
			class="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
		>
			{loadError}
		</p>
	{/if}
	<Card class="border-dashed">
		<CardHeader>
			<CardTitle class="text-base">Client conversation</CardTitle>
			<CardDescription>
				{#if session}
					{session.active ? 'Active' : 'Inactive'} session with {session.clientAlias}
				{:else}
					No chat session has been established for this client.
				{/if}
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-3 text-sm">
			<div class="space-y-2">
				{#if messages.length === 0}
					<p
						class="rounded-lg border border-border/50 bg-muted/30 p-3 text-xs text-muted-foreground"
					>
						{session
							? 'No messages exchanged yet.'
							: 'Start a session to begin chatting with this client.'}
					</p>
				{:else}
					{#each messages as message (message.id)}
						<div
							class={`flex flex-col gap-1 rounded-lg border border-border/60 bg-muted/40 p-3 ${
								message.sender === 'operator' ? 'items-end text-right' : 'items-start text-left'
							}`}
						>
							<div
								class={`flex flex-col ${
									message.sender === 'operator' ? 'items-end text-right' : 'items-start text-left'
								} gap-0.5`}
							>
								<p class="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
									{resolveAlias(message)}
								</p>
								<p class="text-[11px] font-medium tracking-wide text-muted-foreground/80 uppercase">
									{message.sender === 'operator' ? 'Operator' : 'Client'}
								</p>
							</div>
							<p class="text-sm break-words whitespace-pre-wrap text-foreground">{message.body}</p>
							<p class="text-xs text-muted-foreground">
								{formatWorkspaceTimestamp(message.timestamp)}
							</p>
						</div>
					{/each}
				{/if}
			</div>
		</CardContent>
		<CardFooter class="gap-3">
			<Input
				value={draft}
				placeholder="Type a message"
				oninput={(event) => {
					draft = event.currentTarget.value;
					loadError = null;
				}}
				class="flex-1"
			/>
			<Button type="button" variant="outline" onclick={recordDraft}>Save draft</Button>
			<Button type="button" onclick={sendMessage} disabled={dispatching}>
				{#if dispatching}
					Sending…
				{:else}
					Send
				{/if}
			</Button>
		</CardFooter>
	</Card>
</div>
