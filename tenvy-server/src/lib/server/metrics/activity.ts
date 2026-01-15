import { registry } from '$lib/server/rat/store';
import { db } from '$lib/server/db';
import { auditEvent as auditEventTable } from '$lib/server/db/schema';
import type {
	ActivityFlaggedSession,
	ActivityLatencyPoint,
	ActivityModuleEntry,
	ActivitySnapshot,
	ActivitySummaryMetric,
	ActivitySummaryTone,
	ActivityTimelinePoint
} from '$lib/data/activity';
import type { AgentSnapshot } from '../../../../../shared/types/agent';

const WINDOW_MINUTES = 45;
const WINDOW_COUNT = 8;
const DAY_MS = 86_400_000;
const numberFormatter = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 });

type AuditRow = typeof auditEventTable.$inferSelect;

type ParsedAuditRow = {
	agentId: string;
	commandName: string;
	queuedAt: Date | null;
	executedAt: Date | null;
	result: string | null;
};

type ParsedResult = {
	success: boolean | null;
	outputLength: number;
};

function formatNumber(value: number): string {
	return numberFormatter.format(value);
}

function formatDelta(
	value: number,
	comparison: string
): { text: string; tone: ActivitySummaryTone } {
	if (value === 0) {
		return { text: `Stable vs ${comparison}`, tone: 'neutral' };
	}

	const tone: ActivitySummaryTone = value > 0 ? 'positive' : 'warning';
	const sign = value > 0 ? '+' : '−';
	return { text: `${sign}${formatNumber(Math.abs(value))} vs ${comparison}`, tone };
}

function coerceDate(value: unknown): Date | null {
	if (!value) {
		return null;
	}
	const date = value instanceof Date ? value : new Date(value as any);
	return Number.isFinite(date.getTime()) ? date : null;
}

function parseAuditRow(row: AuditRow): ParsedAuditRow {
	return {
		agentId: row.agentId,
		commandName: row.commandName,
		queuedAt: coerceDate(row.queuedAt),
		executedAt: coerceDate(row.executedAt),
		result: row.result ?? null
	} satisfies ParsedAuditRow;
}

function parseResult(value: string | null): ParsedResult {
	if (!value || value.trim().length === 0) {
		return { success: null, outputLength: 0 };
	}

	try {
		const parsed = JSON.parse(value) as {
			success?: unknown;
			output?: unknown;
			error?: unknown;
		};
		const success = typeof parsed.success === 'boolean' ? parsed.success : null;
		const outputBytes =
			typeof parsed.output === 'string' ? Buffer.byteLength(parsed.output, 'utf-8') : 0;
		const errorBytes =
			typeof parsed.error === 'string' ? Buffer.byteLength(parsed.error, 'utf-8') : 0;
		return { success, outputLength: outputBytes + errorBytes };
	} catch {
		return { success: null, outputLength: 0 };
	}
}

function computePercentile(values: number[], percentile: number): number {
	if (values.length === 0) {
		return 0;
	}
	const sorted = [...values].sort((a, b) => a - b);
	const index = (percentile / 100) * (sorted.length - 1);
	const lowerIndex = Math.floor(index);
	const upperIndex = Math.ceil(index);
	const fraction = index - lowerIndex;
	if (lowerIndex === upperIndex) {
		return Math.round(sorted[lowerIndex]);
	}
	const lower = sorted[lowerIndex];
	const upper = sorted[upperIndex];
	return Math.round(lower + (upper - lower) * fraction);
}

function derivePresence(
	agent: AgentSnapshot,
	now: Date
): { status: 'online' | 'idle' | 'dormant' | 'offline'; lastSeen: Date | null } {
	const lastSeen = coerceDate(agent.lastSeen);
	if (agent.status === 'offline') {
		return { status: 'offline', lastSeen };
	}
	if (agent.status === 'error') {
		return { status: 'idle', lastSeen };
	}
	if (lastSeen) {
		const diffMinutes = (now.getTime() - lastSeen.getTime()) / 60_000;
		if (diffMinutes > 240) {
			return { status: 'dormant', lastSeen };
		}
		if (diffMinutes > 10) {
			return { status: 'idle', lastSeen };
		}
	}
	return { status: 'online', lastSeen };
}

function normalizeModuleName(commandName: string | null | undefined): string {
	if (!commandName) {
		return 'Unknown';
	}
	const cleaned = commandName.replace(/[_-]+/g, ' ').replace(/\s+/g, ' ').trim();
	if (!cleaned) {
		return 'Unknown';
	}
	return cleaned.replace(/\b\w/g, (char) => char.toUpperCase());
}

function buildRegion(agent: AgentSnapshot): string {
	const location = agent.metadata.location ?? {};
	const segments = [location.city, location.region, location.country]
		.map((segment) => (typeof segment === 'string' ? segment.trim() : ''))
		.filter((segment) => segment.length > 0);
	const base =
		segments.length > 0 ? segments.join(', ') : (location.countryCode ?? 'Unknown region');
	const ip = agent.metadata.publicIpAddress?.trim() || agent.metadata.ipAddress?.trim();
	return ip ? `${base} • ${ip}` : base;
}

export function buildActivitySnapshot(): ActivitySnapshot {
	const now = new Date();
	const windowMs = WINDOW_MINUTES * 60_000;
	const horizon = new Date(now.getTime() - windowMs * WINDOW_COUNT);

	const agents = registry.listAgents();
	const presence = agents.map((agent) => ({ agent, presence: derivePresence(agent, now) }));

	const rawEvents = db.select().from(auditEventTable).all() as AuditRow[];
	const parsedEvents = rawEvents.map(parseAuditRow);
	const relevantEvents = parsedEvents.filter((event) => {
		const queuedAt = event.queuedAt?.getTime() ?? Number.NEGATIVE_INFINITY;
		const executedAt = event.executedAt?.getTime() ?? Number.NEGATIVE_INFINITY;
		const horizonTime = horizon.getTime();
		return queuedAt >= horizonTime || executedAt >= horizonTime;
	});

	const timeline: ActivityTimelinePoint[] = [];
	const latencyPoints: ActivityLatencyPoint[] = [];
	const moduleStats = new Map<string, { executed: number; queued: number }>();
	const eventsByAgent = new Map<string, ParsedAuditRow[]>();

	for (const event of relevantEvents) {
		if (!eventsByAgent.has(event.agentId)) {
			eventsByAgent.set(event.agentId, []);
		}
		eventsByAgent.get(event.agentId)!.push(event);

		const moduleName = normalizeModuleName(event.commandName);
		const bucket = moduleStats.get(moduleName) ?? { executed: 0, queued: 0 };
		if (event.executedAt) {
			bucket.executed += 1;
		} else {
			bucket.queued += 1;
		}
		moduleStats.set(moduleName, bucket);
	}

	for (let index = 0; index < WINDOW_COUNT; index += 1) {
		const windowEnd = new Date(now.getTime() - (WINDOW_COUNT - 1 - index) * windowMs);
		const windowStart = new Date(windowEnd.getTime() - windowMs);
		const windowStartTime = windowStart.getTime();
		const windowEndTime = windowEnd.getTime();

		const windowEvents = relevantEvents.filter((event) => {
			const queuedAt = event.queuedAt?.getTime();
			if (typeof queuedAt === 'number' && queuedAt >= windowStartTime && queuedAt < windowEndTime) {
				return true;
			}
			const executedAt = event.executedAt?.getTime();
			return (
				typeof executedAt === 'number' &&
				executedAt >= windowStartTime &&
				executedAt < windowEndTime
			);
		});

		let executedCount = 0;
		let queuedCount = 0;
		let suppressedCount = 0;
		const latencySamples: number[] = [];

		for (const event of windowEvents) {
			const parsed = parseResult(event.result);
			if (event.executedAt) {
				executedCount += 1;
				if (parsed.success === false) {
					suppressedCount += 1;
				}
				const queuedAt = event.queuedAt?.getTime();
				const executedAt = event.executedAt.getTime();
				if (typeof queuedAt === 'number') {
					const latency = executedAt - queuedAt;
					if (Number.isFinite(latency) && latency >= 0) {
						latencySamples.push(latency);
					}
				}
			} else {
				queuedCount += 1;
			}
		}

		timeline.push({
			timestamp: windowEnd.toISOString(),
			active: executedCount,
			idle: queuedCount,
			suppressed: suppressedCount
		});

		latencyPoints.push({
			timestamp: windowEnd.toISOString(),
			p50: Math.round(computePercentile(latencySamples, 50)),
			p95: Math.round(computePercentile(latencySamples, 95))
		});
	}

	const moduleActivity: ActivityModuleEntry[] = Array.from(moduleStats.entries())
		.map(([module, stats]) => ({
			module,
			executed: stats.executed,
			queued: stats.queued
		}))
		.sort((a, b) => {
			if (b.executed !== a.executed) {
				return b.executed - a.executed;
			}
			return b.queued - a.queued;
		});

	const flaggedSessions: ActivityFlaggedSession[] = [];
	for (const { agent, presence: presenceEntry } of presence) {
		const agentEvents = eventsByAgent.get(agent.id) ?? [];
		const executedEvents = agentEvents.filter((event) => event.executedAt);
		const failedEvents = executedEvents.filter(
			(event) => parseResult(event.result).success === false
		);
		const backlog = agent.pendingCommands;

		let status: ActivityFlaggedSession['status'] | null = null;
		let reason: string | null = null;

		if (backlog >= 5) {
			status = 'open';
			reason = 'Pending command backlog';
		} else if (failedEvents.length > 0) {
			status = 'review';
			reason = 'Recent command failures';
		} else if (presenceEntry.status === 'offline' && executedEvents.length > 0) {
			status = 'suppressed';
			reason = 'Session dropped after activity';
		}

		if (!status || !reason) {
			continue;
		}

		flaggedSessions.push({
			client: agent.metadata.hostname?.trim() || agent.id,
			reason,
			region: buildRegion(agent),
			interactions: executedEvents.length,
			status
		});
	}

	flaggedSessions.sort((a, b) => {
		if (b.interactions !== a.interactions) {
			return b.interactions - a.interactions;
		}
		return a.client.localeCompare(b.client);
	});

	const liveBeacons = presence.filter((entry) => entry.presence.status === 'online').length;
	const hasLastSeen = presence.some((entry) => entry.presence.lastSeen !== null);
	const previousLiveBeacons = presence.filter((entry) => {
		const lastSeen = entry.presence.lastSeen;
		if (!lastSeen) {
			return false;
		}
		const diff = now.getTime() - lastSeen.getTime();
		return diff >= windowMs && diff < windowMs * 2;
	}).length;
	const liveBeaconDelta: { text: string; tone: ActivitySummaryTone } = hasLastSeen
		? formatDelta(liveBeacons - previousLiveBeacons, 'previous window')
		: { text: 'Telemetry unavailable', tone: 'neutral' };

	const executedTotal = relevantEvents.filter((event) => event.executedAt).length;
	const queuedTotal = relevantEvents.length - executedTotal;
	const tasksDelta =
		executedTotal === 0 && queuedTotal === 0
			? 'No tasks scheduled'
			: `${formatNumber(queuedTotal)} queued downstream`;
	const tasksTone: ActivitySummaryTone = queuedTotal > executedTotal * 0.5 ? 'warning' : 'neutral';

	const escalationsOpen = flaggedSessions.filter((session) => session.status === 'open').length;
	const escalationsReview = flaggedSessions.filter((session) => session.status === 'review').length;
	const escalationsDelta =
		escalationsOpen === 0 && escalationsReview === 0
			? 'No follow-up required'
			: escalationsReview > 0
				? `${formatNumber(escalationsReview)} awaiting analyst review`
				: 'No pending reviews';
	const escalationsTone: ActivitySummaryTone = escalationsOpen > 0 ? 'warning' : 'positive';

	const connectedWindows = agents
		.map((agent) => coerceDate(agent.connectedAt))
		.filter((date): date is Date => date !== null);
	const newClientsToday = connectedWindows.filter(
		(date) => now.getTime() - date.getTime() < DAY_MS
	).length;
	const previousDayClients = connectedWindows.filter((date) => {
		const diff = now.getTime() - date.getTime();
		return diff >= DAY_MS && diff < DAY_MS * 2;
	}).length;
	const newClientsDelta =
		connectedWindows.length === 0
			? { text: 'Telemetry unavailable', tone: 'neutral' as ActivitySummaryTone }
			: newClientsToday === 0 && previousDayClients === 0
				? { text: 'No enrollments recorded', tone: 'neutral' as ActivitySummaryTone }
				: formatDelta(newClientsToday - previousDayClients, 'previous day');

	const summary: ActivitySummaryMetric[] = [
		{
			id: 'live-beacons',
			label: 'Live beacons',
			value: formatNumber(liveBeacons),
			delta: liveBeaconDelta.text,
			tone: liveBeaconDelta.tone
		},
		{
			id: 'tasks-dispatched',
			label: 'Tasks dispatched',
			value: formatNumber(executedTotal),
			delta: tasksDelta,
			tone: tasksTone
		},
		{
			id: 'escalations',
			label: 'Escalations',
			value: `${formatNumber(escalationsOpen)} open`,
			delta: escalationsDelta,
			tone: escalationsTone
		},
		{
			id: 'new-clients',
			label: 'New clients today',
			value: formatNumber(newClientsToday),
			delta: newClientsDelta.text,
			tone: newClientsDelta.tone
		}
	];

	return {
		generatedAt: now.toISOString(),
		summary,
		timeline,
		moduleActivity,
		latency: {
			windowLabel: `Last ${WINDOW_COUNT} intervals`,
			points: latencyPoints,
			granularityMinutes: WINDOW_MINUTES
		},
		flaggedSessions
	} satisfies ActivitySnapshot;
}
