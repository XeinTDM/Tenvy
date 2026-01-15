import { page } from '@vitest/browser/context';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { writable } from 'svelte/store';

import type { DashboardClient, DashboardLogEntry } from '$lib/data/dashboard';

vi.mock('./components/client-presence-map.lazy.svelte', () => ({
	default: class {
		constructor(options: { target?: HTMLElement }) {
			if (options?.target) {
				const placeholder = document.createElement('div');
				placeholder.dataset.testid = 'client-map-stub';
				options.target.appendChild(placeholder);
			}
		}
		$destroy() {}
		$set() {}
	}
}));

import DashboardOperationsPanel from './dashboard-operations-panel.svelte';

describe('dashboard-operations-panel', () => {
	const clients: DashboardClient[] = [
		{
			id: 'client-1',
			codename: 'ALPHA',
			status: 'online',
			connectedAt: new Date().toISOString(),
			lastSeen: new Date().toISOString(),
			metrics: { latencyMs: 120 },
			location: {
				city: 'Austin',
				country: 'United States',
				countryCode: 'US',
				latitude: 30.2672,
				longitude: -97.7431
			}
		}
	];

	const logs: DashboardLogEntry[] = [
		{
			id: 'log-1',
			clientId: 'client-1',
			codename: 'ALPHA',
			timestamp: new Date().toISOString(),
			action: 'connect',
			description: 'Client connected',
			severity: 'info',
			countryCode: 'US'
		}
	];

	it('shows the map view by default and renders logs on demand', async () => {
		let selectedCountry: string | null = null;
		const { unmount } = render(DashboardOperationsPanel, {
			clients,
			logs,
			generatedAt: new Date().toISOString(),
			selectedCountry
		});

		await expect.element(page.getByTestId('client-map-stub')).toBeInTheDocument();

		await page.getByLabelText('Open operations view menu').click();
		await page.getByRole('button', { name: 'Logs' }).click();

		await expect.element(page.getByText('Client connected')).toBeInTheDocument();
		unmount();
	});
});
