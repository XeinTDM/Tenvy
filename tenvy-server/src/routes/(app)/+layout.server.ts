import { redirect } from '@sveltejs/kit';
import type { LayoutServerLoad } from './$types';
import { buildDashboardSnapshot } from '$lib/server/metrics/dashboard';
import { db } from '$lib/server/db';
import { plugin as pluginTable } from '$lib/server/db/schema';
import type { NavKey } from '$lib/types/navigation';

const buildNavBadges = (
	snapshot: ReturnType<typeof buildDashboardSnapshot>
): Partial<Record<NavKey, string>> => {
	const { totals, logs } = snapshot;
	const dashboardBadge =
		totals.total > 0 ? `${totals.connected}/${totals.total}` : totals.connected.toString();
	return {
		dashboard: dashboardBadge,
		clients: String(totals.total),
		activity: String(logs.length)
	};
};

export const load: LayoutServerLoad = ({ locals, url }) => {
	if (!locals.user) {
		throw redirect(303, `/login?redirect=${encodeURIComponent(url.pathname)}`);
	}

	if (!locals.user.passkeyRegistered) {
		throw redirect(303, '/redeem');
	}

	const snapshot = buildDashboardSnapshot();
	locals.dashboardSnapshot = snapshot;

	const pluginCount = db.select({ id: pluginTable.id }).from(pluginTable).all().length;
	const navBadges = {
		...buildNavBadges(snapshot),
		plugins: String(pluginCount)
	};

	return {
		user: locals.user,
		navBadges
	};
};
