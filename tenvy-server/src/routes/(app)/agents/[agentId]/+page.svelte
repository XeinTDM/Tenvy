<script lang="ts">
	import {
		Card,
		CardContent,
		CardDescription,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Separator } from '$lib/components/ui/separator/index.js';
	import type { PageData } from './$types';
	import type { AuditEventSummary } from './+page';

	let { data } = $props<{ data: PageData }>();
	const agent = $derived(data.agent);
	const events = $derived(data.events);

	const formatter = new Intl.DateTimeFormat(undefined, {
		dateStyle: 'medium',
		timeStyle: 'short'
	});

	const statusStyles: Record<'success' | 'failure' | 'pending', string> = {
		success: 'bg-emerald-500/10 text-emerald-600 border border-emerald-500/30',
		failure: 'bg-rose-500/10 text-rose-600 border border-rose-500/30',
		pending: 'bg-amber-500/10 text-amber-600 border border-amber-500/30'
	};

	type ParsedResult = { success: boolean; output?: string | null; error?: string | null } | null;

	function parseResult(value: string | null): ParsedResult {
		if (!value) return null;
		try {
			const parsed = JSON.parse(value) as ParsedResult;
			if (parsed && typeof parsed === 'object') {
				return parsed;
			}
		} catch {
			// fall through to plain text
		}
		return null;
	}

	const enrichedEvents = $derived(
		events.map((event: AuditEventSummary) => {
			const parsed = parseResult(event.result);
			const status: 'pending' | 'success' | 'failure' = !event.executedAt
				? 'pending'
				: parsed
					? parsed.success
						? 'success'
						: 'failure'
					: 'success';

			let summary = 'Awaiting execution';
			if (event.executedAt) {
				if (event.result) {
					if (parsed) {
						summary = (parsed.success
							? (parsed.output ?? 'Command completed successfully')
							: (parsed.error ?? 'Command failed')
						).slice(0, 160);
					} else {
						summary = event.result.slice(0, 160);
					}
				} else {
					summary = 'Completed (no result payload)';
				}
			}

			const statements =
				event.acknowledgement?.statements?.map((s: { text: string }) => s.text) ?? [];

			return {
				...event,
				status,
				summary,
				statements
			};
		})
	);

	function formatTimestamp(value: string | null): string {
		if (!value) {
			return '—';
		}
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) {
			return '—';
		}
		return formatter.format(date);
	}

	function formatOperator(value: string | null): string {
		return value ?? '—';
	}
</script>

<div class="space-y-6">
	<Card>
		<CardHeader class="gap-2">
			<CardTitle class="text-lg">Audit trail</CardTitle>
			<CardDescription>
				Review command activity for <span class="font-semibold text-slate-900 dark:text-slate-100"
					>{agent.metadata.hostname ?? agent.id}</span
				>.
			</CardDescription>
		</CardHeader>
		<CardContent class="space-y-4">
			<div class="grid gap-2 text-sm text-slate-600 sm:grid-cols-2 dark:text-slate-400">
				<div>
					<span class="font-semibold text-slate-900 dark:text-slate-100">Agent ID:</span>
					<span class="ml-2 font-mono text-slate-700 dark:text-slate-300">{agent.id}</span>
				</div>
				<div>
					<span class="font-semibold text-slate-900 dark:text-slate-100">Status:</span>
					<span class="ml-2 text-slate-700 capitalize dark:text-slate-300">{agent.status}</span>
				</div>
				<div>
					<span class="font-semibold text-slate-900 dark:text-slate-100">Queued events:</span>
					<span class="ml-2 text-slate-700 dark:text-slate-300">{events.length}</span>
				</div>
			</div>
			<Separator class="my-2" />
			{#if events.length === 0}
				<p class="text-sm text-slate-600 dark:text-slate-400">
					No command activity has been recorded for this agent yet.
				</p>
			{:else}
				<div class="overflow-x-auto">
					<table
						class="min-w-full divide-y divide-slate-200 text-left text-sm dark:divide-slate-800"
					>
						<thead class="bg-slate-50 dark:bg-slate-900/40">
							<tr class="text-xs tracking-wide text-slate-500 uppercase dark:text-slate-400">
								<th class="px-4 py-3 font-semibold">Command</th>
								<th class="px-4 py-3 font-semibold">Operator</th>
								<th class="px-4 py-3 font-semibold">Status</th>
								<th class="px-4 py-3 font-semibold">Confirmation</th>
								<th class="px-4 py-3 font-semibold">Queued</th>
								<th class="px-4 py-3 font-semibold">Executed</th>
								<th class="px-4 py-3 font-semibold">Payload hash</th>
								<th class="px-4 py-3 font-semibold">Result</th>
							</tr>
						</thead>
						<tbody class="divide-y divide-slate-100 dark:divide-slate-900/60">
							{#each enrichedEvents as event (event.id)}
								<tr
									class="bg-white/70 transition hover:bg-slate-50 dark:bg-slate-900/60 dark:hover:bg-slate-900"
								>
									<td class="px-4 py-3 font-mono text-xs text-slate-700 dark:text-slate-200">
										{event.commandName}
									</td>
									<td class="px-4 py-3 text-xs text-slate-600 dark:text-slate-300">
										{formatOperator(event.operatorId)}
									</td>
									<td class="px-4 py-3">
										<Badge class={statusStyles[event.status as 'pending' | 'success' | 'failure']}
											>{event.status === 'pending'
												? 'Pending'
												: event.status === 'success'
													? 'Succeeded'
													: 'Failed'}</Badge
										>
									</td>
									<td class="px-4 py-3 text-xs text-slate-600 dark:text-slate-300">
										{#if event.acknowledgement}
											<div class="space-y-1">
												<p class="font-medium text-slate-700 dark:text-slate-200">
													{formatTimestamp(event.acknowledgedAt)}
												</p>
												<ul class="ml-4 list-disc space-y-1 text-slate-600 dark:text-slate-300">
													{#each event.statements as statement (statement)}
														<li>{statement}</li>
													{/each}
												</ul>
											</div>
										{:else}
											—
										{/if}
									</td>
									<td class="px-4 py-3 text-xs text-slate-600 dark:text-slate-300">
										{formatTimestamp(event.queuedAt)}
									</td>
									<td class="px-4 py-3 text-xs text-slate-600 dark:text-slate-300">
										{formatTimestamp(event.executedAt)}
									</td>
									<td class="px-4 py-3">
										<code
											class="rounded bg-slate-100 px-2 py-1 font-mono text-[11px] text-slate-700 dark:bg-slate-900/60 dark:text-slate-200"
										>
											{event.payloadHash}
										</code>
									</td>
									<td class="px-4 py-3 text-xs text-slate-600 dark:text-slate-300">
										{event.summary}
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</CardContent>
	</Card>
</div>
