<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
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
		CardFooter,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { getClientTool } from '$lib/data/client-tools';
	import type { Client } from '$lib/data/clients';
	import { appendWorkspaceLog, createWorkspaceLogEntry } from '$lib/workspace/utils';
	import type { WorkspaceLogEntry } from '$lib/workspace/types';
	import { ShieldCheck, TriangleAlert } from '@lucide/svelte';
	import type {
		CommandQueueAuditRecord,
		CommandQueueResponse
	} from '../../../../../../shared/types/messages';

	const { client, onLogChange } = $props<{
		client: Client;
		onLogChange?: (log: WorkspaceLogEntry[]) => void;
	}>();
	void client;

	const tool = getClientTool('open-url');
	void tool;

	let url = $state('https://');
	let referer = $state('');
	let browserChoice = $state<'default' | 'edge' | 'chrome' | 'firefox'>('default');
	let scheduleMinutes = $state(0);
	let note = $state('');
	let log = $state<WorkspaceLogEntry[]>([]);
	let dispatching = $state(false);
	let acknowledgementResult = $state<CommandQueueAuditRecord | null>(null);

	type ChecklistDefinition = {
		id: string;
		label: string | ((client: Client) => string);
		description?: string | ((client: Client) => string);
	};

	const checklistDefinitions: ChecklistDefinition[] = [
		{
			id: 'verify-target',
			label:
				'I inspected the destination host, path, and query parameters for spoofing or tampering.',
			description: 'Confirm the URL belongs to an authorized domain before dispatching the request.'
		},
		{
			id: 'document-justification',
			label: (client) =>
				`I recorded why ${client.codename} should open this link in the operator note field.`,
			description: 'Operator notes are stored with the audit trail for this launch.'
		}
	];

	let checklistState = $state<Record<string, boolean>>(
		Object.fromEntries(checklistDefinitions.map((item) => [item.id, false]))
	);

	const acknowledgementFormatter = new Intl.DateTimeFormat(undefined, {
		dateStyle: 'medium',
		timeStyle: 'short'
	});

	function resolveChecklistLabel(definition: ChecklistDefinition): string {
		return typeof definition.label === 'function' ? definition.label(client) : definition.label;
	}

	function resolveChecklistDescription(definition: ChecklistDefinition): string | null {
		if (!definition.description) {
			return null;
		}
		return typeof definition.description === 'function'
			? definition.description(client)
			: definition.description;
	}

	function setChecklistChecked(id: string, checked: boolean): void {
		checklistState = { ...checklistState, [id]: checked };
	}

	const checklistComplete = $derived(checklistDefinitions.every((item) => checklistState[item.id]));

	function formatAcknowledgementTimestamp(value: string | null | undefined): string {
		if (!value) {
			return '—';
		}
		const parsed = new Date(value);
		if (Number.isNaN(parsed.getTime())) {
			return '—';
		}
		return acknowledgementFormatter.format(parsed);
	}

	function describePlan(): string {
		return `${url} · browser ${browserChoice} · ${scheduleMinutes > 0 ? `delay ${scheduleMinutes}m` : 'run now'}${referer ? ` · referer ${referer}` : ''}`;
	}

	function updateLogEntry(id: string, updates: Partial<WorkspaceLogEntry>) {
		log = log.map((entry) => (entry.id === id ? { ...entry, ...updates } : entry));
	}

	function recordPlan(status: WorkspaceLogEntry['status']) {
		log = appendWorkspaceLog(
			log,
			createWorkspaceLogEntry('URL launch staged', describePlan(), status)
		);
	}

	function recordFailure(message: string) {
		log = appendWorkspaceLog(log, createWorkspaceLogEntry('URL launch failed', message, 'failed'));
	}

	function isValidHttpUrl(candidate: string): boolean {
		try {
			const parsed = new URL(candidate);
			return parsed.protocol === 'http:' || parsed.protocol === 'https:';
		} catch {
			return false;
		}
	}

	async function queueLaunch() {
		if (dispatching) {
			return;
		}

		acknowledgementResult = null;

		const trimmedUrl = url.trim();
		if (!trimmedUrl) {
			recordFailure('Destination URL is required');
			return;
		}

		if (!isValidHttpUrl(trimmedUrl)) {
			recordFailure('Enter a valid http:// or https:// URL');
			return;
		}

		const missingChecklist = checklistDefinitions.filter((item) => !checklistState[item.id]);
		if (missingChecklist.length > 0) {
			recordFailure('Confirm the safety checklist before dispatching the launch');
			return;
		}

		const payload: { url: string; note?: string } = { url: trimmedUrl };
		const noteText = note.trim();
		if (noteText) {
			payload.note = noteText;
		}

		const acknowledgement = {
			confirmedAt: new Date().toISOString(),
			statements: checklistDefinitions.map((definition) => ({
				id: definition.id,
				text: resolveChecklistLabel(definition)
			}))
		};

		const logEntry = createWorkspaceLogEntry('URL launch dispatched', describePlan(), 'queued');
		log = appendWorkspaceLog(log, logEntry);

		dispatching = true;

		try {
			const response = await fetch(`/api/agents/${client.id}/commands`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name: 'open-url', payload, acknowledgement })
			});

			if (!response.ok) {
				const message = (await response.text())?.trim() || 'Failed to queue URL launch';
				updateLogEntry(logEntry.id, {
					status: 'complete',
					detail: message
				});
				return;
			}

			const data = (await response.json()) as CommandQueueResponse;
			const audit: CommandQueueAuditRecord = data?.audit ?? {
				eventId: null,
				acknowledgedAt: acknowledgement.confirmedAt,
				acknowledgement
			};
			acknowledgementResult = audit;
			const delivery = data?.delivery === 'session' ? 'session' : 'queued';
			const detailParts = [
				delivery === 'session' ? 'Launch dispatched to live session' : 'Awaiting agent execution'
			];
			if (audit.eventId) {
				detailParts.push(`audit #${audit.eventId}`);
			} else {
				detailParts.push('audit log updated');
			}
			const detail = detailParts.join(' · ');
			updateLogEntry(logEntry.id, {
				status: 'in-progress',
				detail
			});
			checklistState = Object.fromEntries(checklistDefinitions.map((item) => [item.id, false]));
		} catch (err) {
			const message = err instanceof Error ? err.message : 'Failed to queue URL launch';
			updateLogEntry(logEntry.id, {
				status: 'complete',
				detail: message
			});
		} finally {
			dispatching = false;
		}
	}

	$effect(() => {
		onLogChange?.(log);
	});
</script>

<div class="space-y-6">
	<Card>
		<CardHeader>
			<CardTitle class="text-base">Launch parameters</CardTitle>
			<CardDescription>Define how and when the URL should be opened on the client.</CardDescription>
		</CardHeader>
		<CardContent class="space-y-6">
			<Alert
				class="border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-100"
			>
				<TriangleAlert class="h-4 w-4" />
				<AlertTitle>Review before dispatch</AlertTitle>
				<AlertDescription>
					Opening a remote URL executes on {client.codename}'s device. Confirm the checklist and
					capture your justification before queueing a launch.
				</AlertDescription>
			</Alert>
			<div class="grid gap-2">
				<Label for="open-url-target">URL</Label>
				<Input
					id="open-url-target"
					type="url"
					bind:value={url}
					placeholder="https://target"
					disabled={dispatching}
				/>
			</div>
			<div class="grid gap-4 md:grid-cols-2">
				<div class="grid gap-2">
					<Label for="open-url-browser">Browser</Label>
					<Select
						type="single"
						value={browserChoice}
						onValueChange={(value) => (browserChoice = value as typeof browserChoice)}
					>
						<SelectTrigger id="open-url-browser" class="w-full">
							<span class="capitalize">{browserChoice}</span>
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="default">System default</SelectItem>
							<SelectItem value="edge">Microsoft Edge</SelectItem>
							<SelectItem value="chrome">Google Chrome</SelectItem>
							<SelectItem value="firefox">Mozilla Firefox</SelectItem>
						</SelectContent>
					</Select>
				</div>
				<div class="grid gap-2">
					<Label for="open-url-delay">Delay (minutes)</Label>
					<Input
						id="open-url-delay"
						type="number"
						min={0}
						step={1}
						bind:value={scheduleMinutes}
						disabled={dispatching}
					/>
				</div>
			</div>
			<div class="grid gap-2">
				<Label for="open-url-referer">Referer header</Label>
				<Input
					id="open-url-referer"
					bind:value={referer}
					placeholder="https://source.example"
					disabled={dispatching}
				/>
			</div>
			<div class="grid gap-2">
				<Label for="open-url-note">Operator note</Label>
				<textarea
					id="open-url-note"
					class="min-h-20 w-full rounded-md border border-border/60 bg-background px-3 py-2 text-sm focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
					bind:value={note}
					placeholder="Explain why this URL is being launched."
					disabled={dispatching}
				></textarea>
			</div>
			<div class="space-y-3">
				<p class="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
					Confirmation checklist
				</p>
				<div class="space-y-2">
					{#each checklistDefinitions as item (item.id)}
						{@const checklistId = `open-url-checklist-${item.id}`}
						<label
							class="flex items-start gap-3 rounded-md border border-transparent px-2 py-2 transition hover:border-border/60"
						>
							<Checkbox
								aria-describedby={`${checklistId}-description`}
								aria-label={resolveChecklistLabel(item)}
								checked={Boolean(checklistState[item.id])}
								disabled={dispatching}
								onCheckedChange={(value) => setChecklistChecked(item.id, value === true)}
							/>
							<div class="space-y-1 text-sm leading-relaxed">
								<span class="font-medium text-foreground">{resolveChecklistLabel(item)}</span>
								{#if resolveChecklistDescription(item)}
									<p id={`${checklistId}-description`} class="text-xs text-muted-foreground">
										{resolveChecklistDescription(item)}
									</p>
								{/if}
							</div>
						</label>
					{/each}
				</div>
			</div>
			{#if acknowledgementResult}
				<Alert
					class="border-emerald-200 bg-emerald-50 text-emerald-900 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-100"
				>
					<ShieldCheck class="h-4 w-4" />
					<AlertTitle>Confirmation logged</AlertTitle>
					<AlertDescription>
						<div class="space-y-2">
							<p>
								{#if acknowledgementResult.eventId}
									Recorded as audit event #{acknowledgementResult.eventId}.
								{:else}
									A new audit entry has been recorded.
								{/if}
								Confirmed {formatAcknowledgementTimestamp(
									acknowledgementResult.acknowledgedAt ??
										acknowledgementResult.acknowledgement?.confirmedAt ??
										null
								)}.
							</p>
							{#if acknowledgementResult.acknowledgement?.statements?.length}
								<ul class="list-disc space-y-1 pl-4">
									{#each acknowledgementResult.acknowledgement.statements as statement (statement.id)}
										<li>{statement.text}</li>
									{/each}
								</ul>
							{/if}
						</div>
					</AlertDescription>
				</Alert>
			{/if}
		</CardContent>
		<CardFooter class="flex flex-wrap gap-3">
			<Button type="button" variant="outline" onclick={() => recordPlan('draft')}>Save draft</Button
			>
			<Button type="button" onclick={queueLaunch} disabled={dispatching || !checklistComplete}>
				{#if dispatching}
					Dispatching…
				{:else}
					Queue launch
				{/if}
			</Button>
		</CardFooter>
	</Card>
</div>
