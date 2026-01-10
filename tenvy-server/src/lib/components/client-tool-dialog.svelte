<script lang="ts">
	import { browser } from '$app/environment';
	import { createEventDispatcher, onMount } from 'svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import MovableWindow from '$lib/components/ui/movablewindow/MovableWindow.svelte';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import {
		Select,
		SelectTrigger,
		SelectContent,
		SelectItem
	} from '$lib/components/ui/select/index.js';
	import {
		Card,
		CardContent,
		CardDescription,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import type { Client } from '$lib/data/clients';
	import { getClientTool, type DialogToolId } from '$lib/data/client-tools';
	import {
		getKeyloggerMode,
		getWorkspaceComponent,
		isWorkspaceTool,
		workspaceRequiresAgent,
		type WorkspaceProps
	} from '$lib/data/client-tool-workspaces';
	import { notifyToolActivationCommand } from '$lib/utils/agent-commands.js';
	import KeyloggerWorkspace from '$lib/components/workspace/tools/keylogger-workspace.svelte';
	import NotesWorkspace from '$lib/components/workspace/tools/notes-workspace.svelte';
	import SystemInformationDialog from '$lib/components/system-information-dialog.svelte';
	import { ShieldCheck, TriangleAlert } from '@lucide/svelte';
	import type {
		CommandQueueAuditRecord,
		CommandQueueResponse
	} from '../../../../shared/types/messages';
	import type { AgentSnapshot } from '../../../../shared/types/agent';

	const {
		toolId,
		client,
		agent = null
	} = $props<{
		toolId: DialogToolId;
		client: Client;
		agent?: AgentSnapshot | null;
	}>();

	const dispatch = createEventDispatcher<{ close: void }>();

	let open = $state(true);

	function handleOpenChange(next: boolean) {
		open = next;
	}

	function handleOpenChangeComplete(next: boolean) {
		if (!next) {
			dispatch('close');
		}
	}

	function requestClose() {
		open = false;
	}

	function handleFormSubmit(event: SubmitEvent) {
		event.preventDefault();
		requestClose();
	}

	const tool = getClientTool(toolId);

	const activeWorkspace = getWorkspaceComponent(toolId);
	const keyloggerMode = getKeyloggerMode(toolId);
	const isWorkspaceDialog = isWorkspaceTool(toolId);
	const missingAgent = workspaceRequiresAgent.has(toolId) && !agent;
	const workspaceProps: WorkspaceProps | null = (() => {
		if (!activeWorkspace) {
			return null;
		}

		if (toolId === 'cmd' && !agent) {
			return null;
		}

		const base: WorkspaceProps = { client };

		if (toolId === 'cmd' && agent) {
			base.agent = agent;
		}

		return base;
	})();

	const windowWidth = !isWorkspaceDialog ? 640 : toolId === 'system-monitor' ? 1180 : 980;
	const windowHeight = isWorkspaceDialog ? (toolId === 'system-monitor' ? 720 : 640) : 540;

	onMount(() => {
		if (!browser) {
			return;
		}
		notifyToolActivationCommand(client.id, toolId, {
			action: 'open',
			metadata: { surface: 'dialog' }
		});

		return () => {
			notifyToolActivationCommand(client.id, toolId, {
				action: 'close',
				metadata: { surface: 'dialog' }
			});
		};
	});

	const selectClasses =
		'flex h-9 w-full min-w-0 rounded-md border border-input bg-background px-3 py-1 text-sm shadow-xs ring-offset-background transition-[color,box-shadow] outline-none disabled:cursor-not-allowed disabled:opacity-50 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-input/30';

	let url = $state('https://');
	let urlContext = $state('');
	let messageTitle = $state('');
	let messageBody = $state('');
	type MessageStyle = 'info' | 'warning' | 'critical';
	let messageStyle = $state<MessageStyle>('info');

	let openUrlPending = $state(false);
	let openUrlError = $state<string | null>(null);
	let openUrlAudit = $state<CommandQueueAuditRecord | null>(null);
	let openUrlComplete = $state(false);

	const openUrlFieldId = `client-${client.id}-open-url`;
	const openUrlContextId = `client-${client.id}-open-url-context`;
	const messageTitleId = `client-${client.id}-message-title`;
	const messageBodyId = `client-${client.id}-message-body`;
	const messageStyleId = `client-${client.id}-message-style`;

	type OpenUrlChecklistDefinition = {
		id: string;
		label: string | ((client: Client) => string);
		description?: string | ((client: Client) => string);
	};

	const openUrlChecklistDefinitions: OpenUrlChecklistDefinition[] = [
		{
			id: 'verify-target',
			label:
				'I inspected the destination host, path, and query parameters for spoofing or tampering.',
			description: 'Confirm the URL belongs to an authorized domain before dispatching the request.'
		},
		{
			id: 'document-justification',
			label: (client) =>
				`I documented why ${client.codename} should open this link in the operator note field.`,
			description: 'The operator note becomes part of the permanent audit log for this action.'
		}
	];

	let openUrlChecklistState = $state<Record<string, boolean>>(
		Object.fromEntries(openUrlChecklistDefinitions.map((item) => [item.id, false]))
	);

	const auditTimestampFormatter = new Intl.DateTimeFormat(undefined, {
		dateStyle: 'medium',
		timeStyle: 'short'
	});

	function resolveChecklistLabel(definition: OpenUrlChecklistDefinition): string {
		const value =
			typeof definition.label === 'function' ? definition.label(client) : definition.label;
		return value;
	}

	function resolveChecklistDescription(definition: OpenUrlChecklistDefinition): string | null {
		if (!definition.description) {
			return null;
		}
		return typeof definition.description === 'function'
			? definition.description(client)
			: definition.description;
	}

	function formatAuditAcknowledgement(value: string | null | undefined): string {
		if (!value) {
			return '—';
		}
		const parsed = new Date(value);
		if (Number.isNaN(parsed.getTime())) {
			return '—';
		}
		return auditTimestampFormatter.format(parsed);
	}

	function setChecklistChecked(id: string, checked: boolean): void {
		openUrlChecklistState = { ...openUrlChecklistState, [id]: checked };
	}

	function isValidHttpUrl(candidate: string): boolean {
		try {
			const parsed = new URL(candidate);
			return parsed.protocol === 'http:' || parsed.protocol === 'https:';
		} catch {
			return false;
		}
	}

	async function handleOpenUrlSubmit(event: SubmitEvent) {
		event.preventDefault();

		openUrlError = null;
		openUrlAudit = null;
		openUrlComplete = false;

		const trimmedUrl = url.trim();
		if (!trimmedUrl) {
			openUrlError = 'Destination URL is required';
			return;
		}

		if (!isValidHttpUrl(trimmedUrl)) {
			openUrlError = 'Enter a valid http:// or https:// URL';
			return;
		}

		if (!browser) {
			openUrlError = 'URL dispatch is unavailable in this environment';
			return;
		}

		const missingChecklist = openUrlChecklistDefinitions.filter(
			(item) => !openUrlChecklistState[item.id]
		);
		if (missingChecklist.length > 0) {
			openUrlError = 'Review and confirm the safety checklist before queueing the request';
			return;
		}

		openUrlPending = true;

		const note = urlContext.trim();
		const payload: { url: string; note?: string } = { url: trimmedUrl };
		if (note) {
			payload.note = note;
		}

		const acknowledgement = {
			confirmedAt: new Date().toISOString(),
			statements: openUrlChecklistDefinitions.map((definition) => ({
				id: definition.id,
				text: resolveChecklistLabel(definition)
			}))
		};

		try {
			const response = await fetch(`/api/agents/${client.id}/commands`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name: 'open-url', payload, acknowledgement })
			});

			if (!response.ok) {
				const message = (await response.text())?.trim();
				openUrlError = message || 'Failed to queue open URL request';
				return;
			}

			const data = (await response.json().catch(() => null)) as CommandQueueResponse | null;
			const fallbackAudit: CommandQueueAuditRecord = {
				eventId: null,
				acknowledgedAt: acknowledgement.confirmedAt,
				acknowledgement
			};
			openUrlAudit = data?.audit ?? fallbackAudit;
			openUrlComplete = true;
			url = trimmedUrl;
			urlContext = note;
			openUrlChecklistState = Object.fromEntries(
				openUrlChecklistDefinitions.map((item) => [item.id, false])
			);
		} catch (err) {
			openUrlError = err instanceof Error ? err.message : 'Failed to queue open URL request';
		} finally {
			openUrlPending = false;
		}
	}
</script>

<Dialog.Root
	bind:open
	onOpenChange={handleOpenChange}
	onOpenChangeComplete={handleOpenChangeComplete}
>
	<Dialog.Content
		class="pointer-events-none top-0 left-0 z-50 h-screen w-screen max-w-none translate-x-0 translate-y-0 border-none bg-transparent p-0 shadow-none"
		showCloseButton={false}
	>
		<div class="pointer-events-auto">
			<MovableWindow
				title={tool.title}
				width={windowWidth}
				height={windowHeight}
				onClose={requestClose}
			>
				<div class="flex h-full flex-col bg-background">
					<div
						class="border-b border-border/70 bg-muted/40 px-6 py-4 text-sm text-muted-foreground"
					>
						{tool.description}
					</div>

					{#if isWorkspaceDialog}
						<div class="flex-1 overflow-auto px-6 py-5">
							{#if keyloggerMode}
								<KeyloggerWorkspace {client} mode={keyloggerMode} />
							{:else if missingAgent}
								<Card class="border-dashed">
									<CardHeader>
										<CardTitle>Agent snapshot required</CardTitle>
										<CardDescription>
											Re-open this tool from the clients table to access the latest agent metadata.
										</CardDescription>
									</CardHeader>
								</Card>
							{:else if activeWorkspace && workspaceProps}
								{@const Component = activeWorkspace}
								<Component {...workspaceProps} />
							{:else}
								<Card class="border-dashed">
									<CardHeader>
										<CardTitle>{tool.title}</CardTitle>
										<CardDescription>{tool.description}</CardDescription>
									</CardHeader>
									<CardContent class="space-y-4 text-sm text-muted-foreground">
										<p>
											The workspace for this tool hasn&rsquo;t been implemented yet. Document the
											contract here before wiring it to the Go agent.
										</p>
										<p>
											Add a dedicated workspace component for <span class="font-medium"
												>{tool.title}</span
											>
											to elevate the operator experience when you are ready.
										</p>
									</CardContent>
								</Card>
							{/if}
						</div>
					{:else if toolId === 'system-info'}
						<SystemInformationDialog {client} />
					{:else if toolId === 'notes'}
						<NotesWorkspace {client}>
							{#snippet secondary({ noteSavePending })}
								<Dialog.Close>
									{#snippet child({ props })}
										<Button variant="outline" disabled={noteSavePending} {...props}>Cancel</Button>
									{/snippet}
								</Dialog.Close>
							{/snippet}
						</NotesWorkspace>
					{:else if toolId === 'open-url'}
						<form class="flex h-full flex-col" onsubmit={handleOpenUrlSubmit}>
							<div class="flex-1 space-y-6 overflow-auto px-6 py-5">
								<Alert
									class="border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-100"
								>
									<TriangleAlert class="h-4 w-4" />
									<AlertTitle>Review before dispatch</AlertTitle>
									<AlertDescription>
										Opening a remote URL executes on {client.codename}'s device. Confirm the
										checklist and capture your justification before sending this command.
									</AlertDescription>
								</Alert>
								<div class="grid gap-2">
									<Label for={openUrlFieldId}>Destination URL</Label>
									<Input
										id={openUrlFieldId}
										type="url"
										bind:value={url}
										placeholder="https://target.example.com"
										required
										disabled={openUrlPending}
									/>
								</div>
								<div class="grid gap-2">
									<Label for={openUrlContextId}>Operator note</Label>
									<Textarea
										id={openUrlContextId}
										class="min-h-32"
										bind:value={urlContext}
										placeholder="Document why {client.codename} should open this link."
										disabled={openUrlPending}
									/>
								</div>
								<div class="space-y-3">
									<p class="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
										Confirmation checklist
									</p>
									<div class="space-y-2">
										{#each openUrlChecklistDefinitions as item (item.id)}
											{@const checklistId = `${openUrlContextId}-${item.id}`}
											<label
												class="flex items-start gap-3 rounded-md border border-transparent px-2 py-2 transition hover:border-border/60"
											>
												<Checkbox
													aria-describedby={`${checklistId}-description`}
													aria-label={resolveChecklistLabel(item)}
													checked={Boolean(openUrlChecklistState[item.id])}
													disabled={openUrlPending || openUrlComplete}
													onCheckedChange={(value) => setChecklistChecked(item.id, value === true)}
												/>
												<div class="space-y-1 text-sm leading-relaxed">
													<span class="font-medium text-foreground"
														>{resolveChecklistLabel(item)}</span
													>
													{#if resolveChecklistDescription(item)}
														<p
															id={`${checklistId}-description`}
															class="text-xs text-muted-foreground"
														>
															{resolveChecklistDescription(item)}
														</p>
													{/if}
												</div>
											</label>
										{/each}
									</div>
								</div>
								{#if openUrlError}
									<Alert variant="destructive">
										<AlertTitle>Unable to queue request</AlertTitle>
										<AlertDescription>{openUrlError}</AlertDescription>
									</Alert>
								{/if}
								{#if openUrlAudit}
									<Alert
										class="border-emerald-200 bg-emerald-50 text-emerald-900 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-100"
									>
										<ShieldCheck class="h-4 w-4" />
										<AlertTitle>Confirmation logged</AlertTitle>
										<AlertDescription>
											<div class="space-y-2">
												<p>
													{#if openUrlAudit.eventId}
														Recorded as audit event #{openUrlAudit.eventId}.
													{:else}
														A new audit entry has been recorded.
													{/if}
													Confirmed {formatAuditAcknowledgement(
														openUrlAudit.acknowledgedAt ??
															openUrlAudit.acknowledgement?.confirmedAt ??
															null
													)}.
												</p>
												{#if openUrlAudit.acknowledgement?.statements?.length}
													<ul class="list-disc space-y-1 pl-4">
														{#each openUrlAudit.acknowledgement.statements as statement (statement.id)}
															<li>{statement.text}</li>
														{/each}
													</ul>
												{/if}
											</div>
										</AlertDescription>
									</Alert>
								{/if}
								<p class="text-xs text-muted-foreground">
									Confirmation details and the operator note are stored in the audit trail for {client.codename}.
								</p>
							</div>
							<div
								class="flex items-center justify-end gap-2 border-t border-border/70 bg-muted/30 px-6 py-4"
							>
								<Dialog.Close>
									{#snippet child({ props })}
										<Button variant="outline" {...props}
											>{openUrlComplete ? 'Close' : 'Cancel'}</Button
										>
									{/snippet}
								</Dialog.Close>
								<Button type="submit" disabled={openUrlPending || openUrlComplete}>
									{#if openUrlPending}
										Queueing…
									{:else if openUrlComplete}
										Queued
									{:else}
										Queue launch
									{/if}
								</Button>
							</div>
						</form>
					{:else}
						<form class="flex h-full flex-col" onsubmit={handleFormSubmit}>
							<div class="flex-1 space-y-6 overflow-auto px-6 py-5">
								<div class="grid gap-2">
									<Label for={messageTitleId}>Title</Label>
									<Input
										id={messageTitleId}
										bind:value={messageTitle}
										placeholder="System notice"
									/>
								</div>
								<div class="grid gap-2">
									<Label for={messageBodyId}>Message body</Label>
									<Textarea
										id={messageBodyId}
										class="min-h-32"
										bind:value={messageBody}
										placeholder="Detail the prompt to display on {client.codename}."
										required
									/>
								</div>
								<div class="grid gap-2">
									<Label for={messageStyleId}>Style</Label>
									<Select type="single" bind:value={messageStyle}>
										<SelectTrigger id={messageStyleId} class={selectClasses} />
										<SelectContent>
											<SelectItem value="info">Information</SelectItem>
											<SelectItem value="warning">Warning</SelectItem>
											<SelectItem value="critical">Critical</SelectItem>
										</SelectContent>
									</Select>
								</div>
								<p class="text-xs text-muted-foreground">
									Delivery styling and acknowledgement capture will integrate here in a subsequent
									iteration.
								</p>
							</div>
							<div
								class="flex items-center justify-end gap-2 border-t border-border/70 bg-muted/30 px-6 py-4"
							>
								<Dialog.Close>
									{#snippet child({ props })}
										<Button variant="outline" {...props}>Cancel</Button>
									{/snippet}
								</Dialog.Close>
								<Button type="submit">Queue message</Button>
							</div>
						</form>
					{/if}
				</div>
			</MovableWindow>
		</div>
	</Dialog.Content>
</Dialog.Root>
