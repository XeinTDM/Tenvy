<script lang="ts">
	import Cpu from '@lucide/svelte/icons/cpu';
	import Timer from '@lucide/svelte/icons/timer';
	import Globe from '@lucide/svelte/icons/globe';
	import { Tabs, TabsContent, TabsList, TabsTrigger } from '$lib/components/ui/tabs/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Card, CardContent } from '$lib/components/ui/card/index.js';
	import { notifyToolActivationCommand } from '$lib/utils/agent-commands.js';
	import type { DialogToolId } from '$lib/data/client-tools';
	import type { Client } from '$lib/data/clients';
	import TaskManagerWorkspace from '$lib/components/workspace/tools/task-manager-workspace.svelte';
	import StartupManagerWorkspace from '$lib/components/workspace/tools/startup-manager-workspace.svelte';
	import TcpConnectionsWorkspace from '$lib/components/workspace/tools/tcp-connections-workspace.svelte';

	type ViewKey = 'processes' | 'startup' | 'network';

	const { client, initialView = 'processes' } = $props<{
		client: Client;
		initialView?: ViewKey;
	}>();

	const monitorToolId: DialogToolId = 'system-monitor';

	const viewOptions: {
		value: ViewKey;
		label: string;
		description: string;
		accent: string;
		icon: typeof Cpu;
	}[] = [
		{
			value: 'processes',
			label: 'Processes',
			description: 'CPU, memory, threads and handles',
			accent: 'bg-emerald-400',
			icon: Cpu
		},
		{
			value: 'startup',
			label: 'Startup apps',
			description: 'Boot impact and launch scope',
			accent: 'bg-amber-400',
			icon: Timer
		},
		{
			value: 'network',
			label: 'Network',
			description: 'Open ports and remote peers',
			accent: 'bg-sky-400',
			icon: Globe
		}
	];

	const statusLabels: Record<Client['status'], string> = {
		online: 'Connected',
		idle: 'Idle',
		dormant: 'Dormant',
		offline: 'Offline'
	};

	const statusTone: Record<Client['status'], string> = {
		online: 'bg-emerald-500/90 text-emerald-50 shadow-sm',
		idle: 'bg-amber-400/90 text-amber-950 shadow-sm',
		dormant: 'bg-slate-500/70 text-white shadow-sm',
		offline: 'bg-rose-500/90 text-rose-50 shadow-sm'
	};

	const riskTone: Record<Client['risk'], string> = {
		High: 'bg-rose-500/80 text-rose-50 shadow-sm',
		Medium: 'bg-amber-400/85 text-amber-950 shadow-sm',
		Low: 'bg-emerald-500/80 text-emerald-50 shadow-sm'
	};

	const heroMetrics = [
		{
			label: 'Hostname',
			value: client.hostname ?? client.codename,
			hint: `Codename ${client.codename}`
		},
		{
			label: 'Platform',
			value: client.os,
			hint: `Agent ${client.version}`
		},
		{
			label: 'Location',
			value: client.location,
			hint: `IP ${client.ip}`
		}
	];

	let activeView = $state<ViewKey>(initialView);
	let lastNotifiedView: ViewKey | null = null;

	function getStatusLabel(status: Client['status']): string {
		return statusLabels[status];
	}

	function getStatusTone(status: Client['status']): string {
		return statusTone[status];
	}

	function getRiskTone(risk: Client['risk']): string {
		return riskTone[risk];
	}

	$effect(() => {
		if (activeView === lastNotifiedView) {
			return;
		}
		lastNotifiedView = activeView;
		notifyToolActivationCommand(client.id, monitorToolId, {
			action: 'event:panel-activate',
			metadata: { panel: activeView }
		});
	});
</script>

<div class="flex flex-col gap-6">
	<section
		class="rounded-3xl border border-border/60 bg-gradient-to-br from-primary/10 via-background to-muted/40 p-6 shadow-2xl backdrop-blur-sm dark:from-primary/15 dark:via-slate-950 dark:to-slate-900/60"
	>
		<div class="flex flex-col gap-6 lg:flex-row lg:items-center lg:justify-between">
			<div class="space-y-4">
				<div class="flex flex-wrap items-center gap-3">
					<Badge
						class={`rounded-full px-3 py-1 text-xs font-semibold tracking-wide uppercase ${getStatusTone(client.status)}`}
					>
						{getStatusLabel(client.status)}
					</Badge>
					<Badge
						class={`rounded-full border border-transparent px-3 py-1 text-xs font-semibold tracking-wide uppercase ${getRiskTone(client.risk)}`}
					>
						Risk: {client.risk}
					</Badge>
				</div>
				<div class="space-y-2">
					<h1 class="text-3xl font-semibold tracking-tight text-foreground">System Monitor</h1>
					<p class="text-sm text-muted-foreground">
						Processes, startup impact, and live network connections for
						<span class="font-medium text-foreground"> {client.codename}</span>.
					</p>
					<p class="text-xs text-muted-foreground/80">
						Last contact {client.lastSeen}. Platform insights update automatically while the session
						remains active.
					</p>
				</div>
			</div>
			<div class="grid w-full gap-3 sm:grid-cols-3 lg:w-auto">
				{#each heroMetrics as metric (metric.label)}
					<Card
						class="rounded-2xl border border-white/40 bg-white/80 shadow-inner backdrop-blur dark:border-slate-700/60 dark:bg-slate-900/70"
					>
						<CardContent class="space-y-1.5 px-4 py-3">
							<p
								class="text-[0.65rem] font-semibold tracking-[0.2em] text-muted-foreground uppercase"
							>
								{metric.label}
							</p>
							<p class="text-base font-semibold text-foreground">{metric.value}</p>
							<p class="text-xs text-muted-foreground">{metric.hint}</p>
						</CardContent>
					</Card>
				{/each}
			</div>
		</div>
	</section>

	<Tabs orientation="vertical" bind:value={activeView} class="flex flex-col gap-4">
		<div
			class="flex flex-col gap-6 rounded-3xl border border-border/60 bg-background/70 p-4 shadow-xl backdrop-blur lg:flex-row lg:p-6 dark:bg-slate-950/70"
		>
			<aside class="flex w-full flex-col gap-4 lg:max-w-[260px]">
				<div class="space-y-1">
					<p class="text-xs font-semibold tracking-[0.3em] text-muted-foreground uppercase">
						Views
					</p>
					<p class="text-sm text-muted-foreground">
						Switch between live process analytics, startup governance, and socket activity.
					</p>
				</div>
				<TabsList
					class="flex h-auto flex-col gap-2 rounded-2xl border border-border/50 bg-background/60 p-2 shadow-inner"
				>
					{#each viewOptions as option (option.value)}
						{@const Icon = option.icon}
						{@const isActive = activeView === option.value}
						<TabsTrigger
							value={option.value}
							class={`flex h-auto w-full flex-none flex-col items-start gap-2 rounded-xl border px-4 py-3 text-left text-sm transition hover:border-border/60 hover:bg-muted/60 ${
								isActive
									? 'border-primary/70 bg-primary/10 text-primary shadow-lg'
									: 'border-transparent bg-transparent text-muted-foreground'
							}`}
						>
							<div class="flex w-full items-center justify-between gap-3">
								<div class="flex items-center gap-3">
									<span
										class={`flex size-9 items-center justify-center rounded-xl shadow-inner ${
											isActive ? 'bg-primary/15 text-primary' : 'bg-muted/70 text-muted-foreground'
										}`}
									>
										<Icon class="size-4" />
									</span>
									<div class="space-y-0.5">
										<p
											class={`text-sm leading-tight font-semibold ${isActive ? 'text-primary' : 'text-foreground'}`}
										>
											{option.label}
										</p>
										<p class="text-xs text-muted-foreground">{option.description}</p>
									</div>
								</div>
								<span
									class={`h-2.5 w-2.5 rounded-full ${option.accent} ${
										isActive ? 'opacity-100' : 'opacity-30'
									}`}
								></span>
							</div>
						</TabsTrigger>
					{/each}
				</TabsList>
			</aside>
			<div
				class="flex flex-1 flex-col overflow-hidden rounded-2xl border border-border/50 bg-background/90 shadow-inner"
			>
				<TabsContent value="processes" class="flex h-full flex-col">
					<ScrollArea class="h-full">
						<div class="min-h-full px-6 py-5">
							<TaskManagerWorkspace {client} toolId={monitorToolId} panel="processes" />
						</div>
					</ScrollArea>
				</TabsContent>
				<TabsContent value="startup" class="flex h-full flex-col">
					<ScrollArea class="h-full">
						<div class="min-h-full px-6 py-5">
							<StartupManagerWorkspace {client} toolId={monitorToolId} panel="startup" />
						</div>
					</ScrollArea>
				</TabsContent>
				<TabsContent value="network" class="flex h-full flex-col">
					<ScrollArea class="h-full">
						<div class="min-h-full px-6 py-5">
							<TcpConnectionsWorkspace {client} toolId={monitorToolId} panel="network" />
						</div>
					</ScrollArea>
				</TabsContent>
			</div>
		</div>
	</Tabs>
</div>
