<script lang="ts">
	import DashboardCountryList from './components/dashboard-country-list.svelte';
	import DashboardOperationsPanel from './components/dashboard-operations-panel.svelte';
	import DashboardSummaryCard from './components/dashboard-summary-card.svelte';
	import type { PageData } from './$types';

	let { data } = $props<{ data: PageData }>();

	const percentageFormatter = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 });
	let selectedCountry = $state<string | null>(null);
</script>

<div class="flex h-full min-h-0 flex-1 flex-col gap-6 overflow-hidden">
	<DashboardSummaryCard
		totals={data.totals}
		newClients={data.newClients}
		bandwidth={data.bandwidth}
		latency={data.latency}
		{percentageFormatter}
	/>

	<section class="grid h-full min-h-0 flex-1 auto-rows-fr gap-6 overflow-hidden lg:grid-cols-7">
		<DashboardOperationsPanel
			clients={data.clients}
			logs={data.logs}
			generatedAt={data.generatedAt}
			bind:selectedCountry
		/>
		<DashboardCountryList countries={data.countries} bind:selectedCountry {percentageFormatter} />
	</section>
</div>
