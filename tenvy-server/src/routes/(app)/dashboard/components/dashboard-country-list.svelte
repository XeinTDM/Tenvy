<script lang="ts">
	import { cn } from '$lib/utils.js';
	import { Card, CardContent } from '$lib/components/ui/card/index.js';
	import type { DashboardCountryStat } from '$lib/data/dashboard';

	let {
		countries,
		selectedCountry = $bindable(null),
		percentageFormatter
	}: {
		countries: DashboardCountryStat[];
		selectedCountry: string | null;
		percentageFormatter: Intl.NumberFormat;
	} = $props();

	function toggleCountry(code: string) {
		selectedCountry = selectedCountry === code ? null : code;
	}

	function resolveFlagUrl(code: string | null): string | null {
		if (!code || code.length === 0) {
			return null;
		}
		return `https://flagcdn.com/${code.toLowerCase()}.svg`;
	}
</script>

<Card class="flex h-[min(26rem,65vh)] flex-col border-border/60 lg:col-span-2 lg:h-[32rem]">
	<CardContent class="min-h-0 flex-1 overflow-hidden p-0">
		<div class="h-full flex-1 overflow-y-auto">
			{#if countries.length === 0}
				<div class="flex h-full items-center justify-center p-6 text-sm text-muted-foreground">
					No geography telemetry is available.
				</div>
			{:else}
				<div class="divide-y divide-border/60">
					{#each countries as country (country.countryCode)}
						{@const flagUrl = resolveFlagUrl(country.countryCode)}
						<button
							type="button"
							class={cn(
								'flex w-full items-center justify-between gap-3 px-6 py-3 text-left transition-colors',
								selectedCountry === country.countryCode ? 'bg-primary/10' : 'hover:bg-primary/5'
							)}
							onclick={() => toggleCountry(country.countryCode)}
						>
							<div class="flex items-center gap-3">
								{#if flagUrl}
									<img
										src={flagUrl}
										alt=""
										class="h-5 w-8 rounded-sm border border-border/60 object-cover"
										loading="lazy"
									/>
								{:else}
									<span class="text-lg leading-none" aria-hidden="true">{country.flag}</span>
								{/if}
								<div class="space-y-0.5">
									<p class="text-sm font-semibold text-foreground">{country.countryName}</p>
								</div>
							</div>
							<span
								class={cn(
									'rounded-md border px-2 py-0.5 text-xs font-medium',
									selectedCountry === country.countryCode
										? 'border-primary/60 text-primary'
										: 'border-border/60 text-muted-foreground'
								)}
							>
								<p class="text-xs text-muted-foreground">
									{percentageFormatter.format(country.percentage)}%
								</p>
							</span>
						</button>
					{/each}
				</div>
			{/if}
		</div>
	</CardContent>
</Card>
