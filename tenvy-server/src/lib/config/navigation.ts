import {
	Activity,
	Hammer,
	LayoutDashboard,
	PlugZap,
	Users
} from '@lucide/svelte';
import type { IconComponent, NavKey } from '$lib/types/navigation.js';

export type NavHref = '/dashboard' | '/clients' | '/build' | '/activity' | '/plugins';

export type NavItem = {
	title: string;
	icon: IconComponent;
	badgeClass?: string;
	slug: NavKey;
	href: NavHref;
};

export const navItems: NavItem[] = [
	{
		title: 'Dashboard',
		icon: LayoutDashboard,
		badgeClass: 'bg-emerald-500/20 text-emerald-500',
		slug: 'dashboard',
		href: '/dashboard'
	},
	{
		title: 'Clients',
		icon: Users,
		badgeClass: 'bg-blue-500/15 text-blue-500',
		slug: 'clients',
		href: '/clients'
	},
	{
		title: 'Build',
		icon: Hammer,
		slug: 'build',
		href: '/build'
	},
	{
		title: 'Activity',
		icon: Activity,
		badgeClass: 'bg-sidebar-primary/10 text-sidebar-primary',
		slug: 'activity',
		href: '/activity'
	},
	{
		title: 'Plugins',
		icon: PlugZap,
		badgeClass: 'bg-purple-500/15 text-purple-500',
		slug: 'plugins',
		href: '/plugins'
	}
];

export const navSummaries: Record<NavKey, { title: string; description: string }> = {
	dashboard: {
		title: 'Dashboard',
		description: 'Monitor connected agents, watch map & logs, and more.'
	},
	clients: {
		title: 'Clients',
		description:
			'Inspect connected endpoints, filter by posture, and triage which agents need attention next.'
	},
	build: {
		title: 'Builder',
		description: 'Compile customized client binaries and distribute them to targets.'
	},
	activity: {
		title: 'Activity',
		description: 'Streaming event timelines and operation history.'
	},
	plugins: {
		title: 'Plugins',
		description: 'Manage extensions and modular capabilities for the platform.'
	},
	settings: {
		title: 'Settings',
		description: 'Global preferences and administrative configuration.'
	}
};
