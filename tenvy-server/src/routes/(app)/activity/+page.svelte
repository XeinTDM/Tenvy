<script lang="ts">
	import {
		Card,
		CardAction,
		CardContent,
		CardDescription,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import {
		ChartContainer,
		ChartTooltip,
		type ChartConfig
	} from '$lib/components/ui/chart/index.js';
	import { AreaChart, BarChart, LineChart } from 'layerchart';
	import type { PageData } from './$types';
	import type {
		ActivityFlaggedSession,
		ActivitySummaryMetric,
		ActivitySummaryTone
	} from '$lib/data/activity';

	let { data } = $props<{ data: PageData }>();

	const summaryMetrics = $derived(data.summary);

	const summaryToneClasses: Record<ActivitySummaryTone, string> = {
		positive: 'text-emerald-500',
		warning: 'text-amber-500',
		neutral: 'text-muted-foreground'
	};

	const enrichedSummaryMetrics = $derived(
		summaryMetrics.map((metric: ActivitySummaryMetric) => ({
			...metric,
			toneClass: summaryToneClasses[metric.tone]
		}))
	);

	const flaggedStatusMeta: Record<
		ActivityFlaggedSession['status'],
		{ label: string; badgeClass: string }
	> = {
		open: {
			label: 'Open escalation',
			badgeClass: 'bg-amber-500/10 border-amber-500/40 text-amber-500'
		},
		review: {
			label: 'Needs review',
			badgeClass: 'bg-sky-500/10 border-sky-500/40 text-sky-500'
		},
		suppressed: {
			label: 'Suppressed',
			badgeClass: 'bg-muted/60 border-border/60 text-muted-foreground'
		}
	};

	const timeFormatter = new Intl.DateTimeFormat(undefined, {
		hour: '2-digit',
		minute: '2-digit'
	});

	const numberFormatter = new Intl.NumberFormat(undefined, {
		maximumFractionDigits: 0
	});

	type ActivityPoint = {
		timestamp: Date;
		active: number;
		idle: number;
		suppressed: number;
	};

	type TimelineEntry = PageData['timeline'][number];

	const activityTimeline = $derived(
		data.timeline.map(
			(point: TimelineEntry): ActivityPoint => ({
				...point,
				timestamp: new Date(point.timestamp)
			})
		)
	);

	const activityChartConfig = {
		active: {
			label: 'Active beacons',
			theme: {
				light: 'var(--chart-1)',
				dark: 'var(--chart-1)'
			}
		},
		idle: {
			label: 'Idle sleepers',
			theme: {
				light: 'var(--chart-2)',
				dark: 'var(--chart-2)'
			}
		},
		suppressed: {
			label: 'Suppressed links',
			theme: {
				light: 'var(--chart-3)',
				dark: 'var(--chart-3)'
			}
		}
	} satisfies ChartConfig;

	const activitySeries = [
		{
			key: 'active',
			label: activityChartConfig.active.label,
			value: (point: ActivityPoint) => point.active,
			color: 'var(--color-active)'
		},
		{
			key: 'idle',
			label: activityChartConfig.idle.label,
			value: (point: ActivityPoint) => point.idle,
			color: 'var(--color-idle)'
		},
		{
			key: 'suppressed',
			label: activityChartConfig.suppressed.label,
			value: (point: ActivityPoint) => point.suppressed,
			color: 'var(--color-suppressed)'
		}
	];

	const hasTimelineData = $derived(
		activityTimeline.some(
			(point: ActivityPoint) => point.active > 0 || point.idle > 0 || point.suppressed > 0
		)
	);

	type ModuleActivityEntry = PageData['moduleActivity'][number];

	const moduleActivity = $derived(data.moduleActivity);

	const hasModuleTelemetry = $derived(
		moduleActivity.some((entry: ModuleActivityEntry) => entry.executed > 0 || entry.queued > 0)
	);

	const moduleChartConfig = {
		executed: {
			label: 'Executed tasks',
			theme: {
				light: 'var(--chart-4)',
				dark: 'var(--chart-4)'
			}
		},
		queued: {
			label: 'Queued tasks',
			theme: {
				light: 'var(--chart-5)',
				dark: 'var(--chart-5)'
			}
		}
	} satisfies ChartConfig;

	const moduleSeries = [
		{
			key: 'executed',
			label: moduleChartConfig.executed.label,
			value: (entry: ModuleActivityEntry) => entry.executed,
			color: 'var(--color-executed)'
		},
		{
			key: 'queued',
			label: moduleChartConfig.queued.label,
			value: (entry: ModuleActivityEntry) => entry.queued,
			color: 'var(--color-queued)'
		}
	];

	type LatencyPoint = {
		timestamp: Date;
		p50: number;
		p95: number;
	};

	type LatencyEntry = PageData['latency']['points'][number];

	const latencyTrend = $derived(
		data.latency.points.map(
			(entry: LatencyEntry): LatencyPoint => ({
				...entry,
				timestamp: new Date(entry.timestamp)
			})
		)
	);

	const hasLatencySamples = $derived(
		latencyTrend.some((entry: LatencyPoint) => entry.p50 > 0 || entry.p95 > 0)
	);

	const latencyChartConfig = {
		p50: {
			label: 'P50 latency',
			theme: {
				light: 'var(--chart-2)',
				dark: 'var(--chart-2)'
			}
		},
		p95: {
			label: 'P95 latency',
			theme: {
				light: 'var(--chart-1)',
				dark: 'var(--chart-1)'
			}
		}
	} satisfies ChartConfig;

	const latencySeries = [
		{
			key: 'p50',
			label: latencyChartConfig.p50.label,
			value: (entry: LatencyPoint) => entry.p50,
			color: 'var(--color-p50)'
		},
		{
			key: 'p95',
			label: latencyChartConfig.p95.label,
			value: (entry: LatencyPoint) => entry.p95,
			color: 'var(--color-p95)'
		}
	];

	const flaggedSessions = $derived(data.flaggedSessions);

	const enrichedFlaggedSessions = $derived(
		flaggedSessions.map((session: ActivityFlaggedSession) => ({
			...session,
			meta: flaggedStatusMeta[session.status]
		}))
	);
</script>

<section class="space-y-6">
	<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
		{#each enrichedSummaryMetrics as metric (metric.id)}
			<Card class="border-border/60">
				<CardHeader class="space-y-2">
					<CardTitle class="text-sm font-medium text-muted-foreground">{metric.label}</CardTitle>
					<div class="text-2xl font-semibold">{metric.value}</div>
					<p class={`text-xs ${metric.toneClass}`}>{metric.delta}</p>
				</CardHeader>
			</Card>
		{/each}
	</div>

	<div class="grid gap-6 xl:grid-cols-7">
		<Card class="xl:col-span-4">
			<CardHeader class="space-y-1.5">
				<CardTitle>Beacon activity over time</CardTitle>
				<CardDescription>
					Aggregated connection states for the most active clients in the last eight windows.
				</CardDescription>
			</CardHeader>
			<CardContent>
				{#if hasTimelineData}
					<ChartContainer config={activityChartConfig} class="w-full">
						<AreaChart
							data={activityTimeline}
							x={(point) => point.timestamp}
							series={activitySeries}
							seriesLayout="stack"
							props={{
								xAxis: {
									format: (value) => (value instanceof Date ? timeFormatter.format(value) : '')
								},
								yAxis: {
									format: (value) => numberFormatter.format(Number(value ?? 0))
								},
								legend: {
									placement: 'top-right'
								}
							}}
						>
							{#snippet tooltip()}
								<ChartTooltip labelKey="label" indicator="line" />
							{/snippet}
						</AreaChart>
					</ChartContainer>
				{:else}
					<div
						class="rounded-lg border border-dashed border-border/60 p-6 text-center text-sm text-muted-foreground"
					>
						No command activity has been recorded for this period.
					</div>
				{/if}
			</CardContent>
		</Card>

		<Card class="xl:col-span-3">
			<CardHeader>
				<CardTitle>Command latency percentiles</CardTitle>
				<CardDescription>
					Monitors round-trip times captured from controller to clients.
				</CardDescription>
				<CardAction>
					<Badge variant="secondary" class="font-mono text-[0.65rem]">
						{data.latency.windowLabel}
					</Badge>
				</CardAction>
			</CardHeader>
			<CardContent>
				{#if hasLatencySamples}
					<ChartContainer config={latencyChartConfig} class="w-full">
						<LineChart
							data={latencyTrend}
							x={(entry) => entry.timestamp}
							series={latencySeries}
							props={{
								xAxis: {
									format: (value) => (value instanceof Date ? timeFormatter.format(value) : '')
								},
								yAxis: {
									format: (value) => `${numberFormatter.format(Number(value ?? 0))} ms`
								}
							}}
						>
							{#snippet tooltip()}
								<ChartTooltip indicator="line" />
							{/snippet}
						</LineChart>
					</ChartContainer>
				{:else}
					<div
						class="rounded-lg border border-dashed border-border/60 p-6 text-center text-sm text-muted-foreground"
					>
						No latency samples are available for this window yet.
					</div>
				{/if}
			</CardContent>
		</Card>
	</div>

	<div class="grid gap-6 lg:grid-cols-7">
		<Card class="lg:col-span-4">
			<CardHeader class="space-y-1.5">
				<CardTitle>Module execution mix</CardTitle>
				<CardDescription>
					Compares executed jobs against queued follow-ups for the busiest operator pipelines.
				</CardDescription>
			</CardHeader>
			<CardContent>
				{#if hasModuleTelemetry}
					<ChartContainer config={moduleChartConfig} class="w-full">
						<BarChart
							data={moduleActivity}
							x={(entry) => entry.module}
							series={moduleSeries}
							seriesLayout="stack"
							bandPadding={0.3}
							props={{
								yAxis: {
									format: (value) => numberFormatter.format(Number(value ?? 0))
								}
							}}
						>
							{#snippet tooltip()}
								<ChartTooltip />
							{/snippet}
						</BarChart>
					</ChartContainer>
				{:else}
					<div
						class="rounded-lg border border-dashed border-border/60 p-6 text-center text-sm text-muted-foreground"
					>
						No module execution telemetry is available yet.
					</div>
				{/if}
			</CardContent>
		</Card>

		<Card class="lg:col-span-3">
			<CardHeader class="space-y-1">
				<CardTitle>Clients needing attention</CardTitle>
				<CardDescription>
					Signals escalations and high-volume sessions that may require intervention.
				</CardDescription>
			</CardHeader>
			<CardContent class="space-y-4">
				{#if enrichedFlaggedSessions.length === 0}
					<div
						class="rounded-lg border border-dashed border-border/60 p-6 text-center text-sm text-muted-foreground"
					>
						No sessions are currently flagged for review.
					</div>
				{:else}
					{#each enrichedFlaggedSessions as session (session.client)}
						<div class="rounded-lg border border-border/60 p-4">
							<div class="flex items-start justify-between gap-4">
								<div class="space-y-1">
									<p class="text-sm font-medium tracking-[0.08em] text-muted-foreground uppercase">
										{session.client}
									</p>
									<p class="text-sm text-foreground">{session.reason}</p>
									<p class="text-xs text-muted-foreground">{session.region}</p>
								</div>
								<div class="flex flex-col items-end gap-2 text-right">
									<Badge variant="outline" class="font-mono text-xs">
										{numberFormatter.format(session.interactions)} ops
									</Badge>
									<Badge
										variant="outline"
										class={`text-[0.65rem] ${session.meta.badgeClass}`}
									>
										{session.meta.label}
									</Badge>
								</div>
							</div>
						</div>
					{/each}
				{/if}
			</CardContent>
		</Card>
	</div>
</section>
