import { render } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import { writable } from 'svelte/store';

import DashboardCountryList from './dashboard-country-list.svelte';

const percentageFormatter = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 });

describe('dashboard-country-list', () => {
	it('renders a fallback when no countries are available', () => {
		const { getByText } = render(DashboardCountryList, {
			countries: [],
			selectedCountry: null,
			percentageFormatter
		});

		expect(getByText('No geography telemetry is available.')).toBeInTheDocument();
	});
});
