/**
 * @vitest-environment jsdom
 */
import { render } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

import ActivityPage from './+page.svelte';
import type { PageData } from './$types';

vi.mock('layerchart', () => ({
	AreaChart: vi.fn(),
	BarChart: vi.fn(),
	LineChart: vi.fn(),
	getTooltipContext: vi.fn(),
	Tooltip: vi.fn()
}));

describe('activity page telemetry fallbacks', () => {
	it('renders fallback messaging when telemetry is unavailable', () => {
		const snapshot: PageData = {
			activeNav: 'activity',
			generatedAt: new Date().toISOString(),
			summary: [
				{
					id: 'live-beacons',
					label: 'Live beacons',
					value: '0',
					delta: 'Telemetry unavailable',
					tone: 'neutral'
				}
			],
			timeline: [],
			moduleActivity: [],
			latency: { windowLabel: 'Test window', points: [], granularityMinutes: 45 },
			flaggedSessions: []
		};

		const { getByText } = render(ActivityPage, { props: { data: snapshot } });

		expect(getByText('No command activity has been recorded for this period.')).toBeInTheDocument();
		expect(getByText('No latency samples are available for this window yet.')).toBeInTheDocument();
		expect(getByText('No module execution telemetry is available yet.')).toBeInTheDocument();
		expect(getByText('No sessions are currently flagged for review.')).toBeInTheDocument();
	});
});
