export type ClientStatus = 'online' | 'idle' | 'dormant' | 'offline';
export type ClientPlatform = 'windows' | 'linux' | 'macos';
export type ClientRisk = 'Low' | 'Medium' | 'High';

export type Client = {
	id: string;
	codename: string;
	hostname: string;
	ip: string;
	location: string;
	os: string;
	platform: ClientPlatform;
	version: string;
	status: ClientStatus;
	lastSeen: string;
	tags: string[];
	risk: ClientRisk;
	notes?: string;
	noteTags?: string[];
	noteUpdatedAt?: string | null;
	noteUpdatedBy?: string | null;
};

export const statusLabels: Record<ClientStatus, string> = {
	online: 'Online',
	idle: 'Idle',
	dormant: 'Dormant',
	offline: 'Offline'
};

export const statusStyles: Record<ClientStatus, string> = {
	online: 'border border-emerald-500/20 bg-emerald-500/10 text-emerald-600',
	idle: 'border border-sky-500/20 bg-sky-500/10 text-sky-600',
	dormant: 'border border-amber-500/20 bg-amber-500/10 text-amber-600',
	offline: 'border border-slate-500/20 bg-slate-500/10 text-slate-600'
};

export const riskStyles: Record<ClientRisk, string> = {
	Low: 'border border-emerald-500/20 bg-emerald-500/10 text-emerald-600',
	Medium: 'border border-amber-500/20 bg-amber-500/10 text-amber-600',
	High: 'border border-red-500/20 bg-red-500/10 text-red-600'
};

export const statusSummaryOrder: ClientStatus[] = ['online', 'idle', 'offline'];