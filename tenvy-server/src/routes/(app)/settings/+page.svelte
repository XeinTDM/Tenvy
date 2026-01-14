<script lang="ts">
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import {
		Card,
		CardContent,
		CardDescription,
		CardFooter,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Select, SelectTrigger, SelectContent, SelectItem } from '$lib/components/ui/select/index.js';
	import { Separator } from '$lib/components/ui/separator/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import { TriangleAlert, Check, Clock, ShieldCheck, UserPlus } from '@lucide/svelte';

	type UserRole = 'viewer' | 'operator' | 'developer' | 'admin';

	type MemberRecord = {
		id: string;
		role: UserRole;
		voucherId: string;
		createdAt: string;
		voucherExpiresAt: string | null;
		voucherRedeemedAt: string | null;
	};

	type EnrollmentTokenRecord = {
		token: string;
		createdAt: string;
		expiresAt: string | null;
		createdBy: string | null;
		maxUses: number;
		uses: number;
		revokedAt: string | null;
		memo: string | null;
	};

	let {
		data
	}: { data: { members: MemberRecord[]; enrollmentTokens: EnrollmentTokenRecord[] } } = $props();

	const roleOptions: { label: string; value: UserRole }[] = [
		{ label: 'Viewer', value: 'viewer' },
		{ label: 'Operator', value: 'operator' },
		{ label: 'Developer', value: 'developer' },
		{ label: 'Administrator', value: 'admin' }
	];

	let members = $state<MemberRecord[]>(data.members ?? []);
	let enrollmentTokens = $state<EnrollmentTokenRecord[]>(data.enrollmentTokens ?? []);

	$effect(() => {
		members = data.members ?? [];
		enrollmentTokens = data.enrollmentTokens ?? [];
	});

	async function revokeToken(token: string) {
		try {
			const response = await fetch(`/api/admin/tokens/${token}`, {
				method: 'DELETE'
			});

			if (!response.ok) throw new Error('Revocation failed');

			enrollmentTokens = enrollmentTokens.map((t) =>
				t.token === token ? { ...t, revokedAt: new Date().toISOString() } : t
			);
		} catch (err) {
			console.error('Failed to revoke token', err);
		}
	}

	async function createToken() {
		try {
			const response = await fetch('/api/admin/tokens', {
				method: 'POST',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({
					memo: 'Manual generation',
					maxUses: 1,
					expiresInHours: 24
				})
			});

			if (!response.ok) throw new Error('Generation failed');

			const newToken = (await response.json()) as EnrollmentTokenRecord;
			enrollmentTokens = [newToken, ...enrollmentTokens];
		} catch (err) {
			console.error('Failed to generate token', err);
		}
	}

	async function setMemberRole(memberId: string, role: UserRole) {
		const previous = members;
		members = members.map((member) => (member.id === memberId ? { ...member, role } : member));

		try {
			const response = await fetch(`/api/admin/users/${memberId}/role`, {
				method: 'PATCH',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({ role })
			});

			if (!response.ok) {
				const message = await response.text().catch(() => null);
				throw new Error(message ?? 'Role update failed');
			}
		} catch (err) {
			console.error('Failed to update member role', err);
			members = previous;
		}
	}

	type GeneralSettings = {
		organizationName: string;
		controlPlaneHost: string;
		maintenanceWindow: string;
		autoUpdate: boolean;
		allowBeta: boolean;
	};

	type Digest = '15m' | 'hourly' | 'daily';

	type NotificationSettings = {
		realtimeOps: boolean;
		escalateCritical: boolean;
		digestFrequency: Digest;
		emailBridge: boolean;
		slackBridge: boolean;
	};

	type SecuritySettings = {
		enforceMfa: boolean;
		sessionTimeoutMinutes: number;
		ipAllowlist: boolean;
		requireApproval: boolean;
		commandQuorum: number;
	};

	const clone = <T,>(v: T): T => structuredClone(v);

	const initialSettings = {
		general: {
			organizationName: 'Tenvy Operator Group',
			controlPlaneHost: 'relay.tenvy.local',
			maintenanceWindow: 'Sundays 02:00 UTC',
			autoUpdate: true,
			allowBeta: false
		},
		notifications: {
			realtimeOps: true,
			escalateCritical: true,
			digestFrequency: 'hourly' as Digest,
			emailBridge: true,
			slackBridge: false
		},
		security: {
			enforceMfa: true,
			sessionTimeoutMinutes: 30,
			ipAllowlist: true,
			requireApproval: true,
			commandQuorum: 2
		}
	};

	let saved = $state(clone(initialSettings));

	let generalOrganizationName = $state(saved.general.organizationName);
	let generalControlPlaneHost = $state(saved.general.controlPlaneHost);
	let generalMaintenanceWindow = $state(saved.general.maintenanceWindow);
	let generalAutoUpdate = $state(saved.general.autoUpdate);
	let generalAllowBeta = $state(saved.general.allowBeta);

	let notificationsRealtimeOps = $state(saved.notifications.realtimeOps);
	let notificationsEscalateCritical = $state(saved.notifications.escalateCritical);
	let notificationsDigestFrequency = $state<Digest>(saved.notifications.digestFrequency);
	let notificationsEmailBridge = $state(saved.notifications.emailBridge);
	let notificationsSlackBridge = $state(saved.notifications.slackBridge);

	let securityEnforceMfa = $state(saved.security.enforceMfa);
	let securitySessionTimeoutMinutes = $state(saved.security.sessionTimeoutMinutes);
	let securityIpAllowlist = $state(saved.security.ipAllowlist);
	let securityRequireApproval = $state(saved.security.requireApproval);
	let securityCommandQuorum = $state(saved.security.commandQuorum);

	const digestOptions: { label: string; value: Digest }[] = [
		{ label: 'Every 15 minutes', value: '15m' },
		{ label: 'Hourly digest', value: 'hourly' },
		{ label: 'Daily summary', value: 'daily' }
	];

	const general = $derived({
		organizationName: generalOrganizationName,
		controlPlaneHost: generalControlPlaneHost,
		maintenanceWindow: generalMaintenanceWindow,
		autoUpdate: generalAutoUpdate,
		allowBeta: generalAllowBeta
	});

	const notifications = $derived({
		realtimeOps: notificationsRealtimeOps,
		escalateCritical: notificationsEscalateCritical,
		digestFrequency: notificationsDigestFrequency,
		emailBridge: notificationsEmailBridge,
		slackBridge: notificationsSlackBridge
	});

	const security = $derived({
		enforceMfa: securityEnforceMfa,
		sessionTimeoutMinutes: securitySessionTimeoutMinutes,
		ipAllowlist: securityIpAllowlist,
		requireApproval: securityRequireApproval,
		commandQuorum: securityCommandQuorum
	});

	let lastSavedLabel = $state('Never');

	const formatTimestamp = (value: string | null) => {
		if (!value) return '—';
		const parsed = new Date(value);
		if (Number.isNaN(parsed.getTime())) return '—';
		return parsed.toLocaleString();
	};

	function setGeneralFrom(v: GeneralSettings) {
		generalOrganizationName = v.organizationName;
		generalControlPlaneHost = v.controlPlaneHost;
		generalMaintenanceWindow = v.maintenanceWindow;
		generalAutoUpdate = v.autoUpdate;
		generalAllowBeta = v.allowBeta;
	}

	function setNotificationsFrom(v: NotificationSettings) {
		notificationsRealtimeOps = v.realtimeOps;
		notificationsEscalateCritical = v.escalateCritical;
		notificationsDigestFrequency = v.digestFrequency;
		notificationsEmailBridge = v.emailBridge;
		notificationsSlackBridge = v.slackBridge;
	}

	function setSecurityFrom(v: SecuritySettings) {
		securityEnforceMfa = v.enforceMfa;
		securitySessionTimeoutMinutes = v.sessionTimeoutMinutes;
		securityIpAllowlist = v.ipAllowlist;
		securityRequireApproval = v.requireApproval;
		securityCommandQuorum = v.commandQuorum;
	}

	function resetSection(section: keyof typeof saved) {
		if (section === 'general') setGeneralFrom(saved.general);
		else if (section === 'notifications') setNotificationsFrom(saved.notifications);
		else if (section === 'security') setSecurityFrom(saved.security);
	}

	function restoreDefaults() {
		setGeneralFrom(initialSettings.general);
		setNotificationsFrom(initialSettings.notifications);
		setSecurityFrom(initialSettings.security);
	}

	function saveChanges() {
		saved.general = clone(general);
		saved.notifications = clone(notifications);
		saved.security = clone(security);
		lastSavedLabel = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
	}

	const generalDirty = $derived(JSON.stringify(general) !== JSON.stringify(saved.general));
	const notificationsDirty = $derived(JSON.stringify(notifications) !== JSON.stringify(saved.notifications));
	const securityDirty = $derived(JSON.stringify(security) !== JSON.stringify(saved.security));
	const hasChanges = $derived(generalDirty || notificationsDirty || securityDirty);
</script>

<section class="space-y-6">
	<Card class="border-border/60">
		<CardHeader class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
			<div>
				<CardTitle>Team access control</CardTitle>
				<CardDescription>
					Promote developers after voucher redemption and manage operator privileges.
				</CardDescription>
			</div>
			<Badge variant="secondary" class="gap-2">
				<UserPlus class="h-4 w-4" />
				{members.length} seat{members.length === 1 ? '' : 's'}
			</Badge>
		</CardHeader>
		<CardContent class="overflow-x-auto">
			<table class="min-w-full divide-y divide-border text-sm">
				<thead class="bg-muted/40 text-xs tracking-wide text-muted-foreground uppercase">
					<tr>
						<th class="px-4 py-2 text-left">User ID</th>
						<th class="px-4 py-2 text-left">Voucher</th>
						<th class="px-4 py-2 text-left">Role</th>
						<th class="px-4 py-2 text-left">Redeemed</th>
						<th class="px-4 py-2 text-left">Expires</th>
					</tr>
				</thead>
				<tbody class="divide-y divide-border">
					{#if members.length === 0}
						<tr>
							<td colspan="5" class="px-4 py-6 text-center text-muted-foreground">
								No operators have redeemed vouchers yet.
							</td>
						</tr>
					{:else}
						{#each members as member (member.id)}
							<tr>
								<td class="px-4 py-3 font-mono text-xs">{member.id}</td>
								<td class="px-4 py-3">
									<span class="font-mono text-xs text-muted-foreground">
										{member.voucherId}
									</span>
								</td>
								<td class="px-4 py-3">
									<Select
										type="single"
										bind:value={member.role}
										onValueChange={(value) => setMemberRole(member.id, value as UserRole)}
									>
										<SelectTrigger>
											<span class="capitalize">{member.role}</span>
										</SelectTrigger>

										<SelectContent>
											{#each roleOptions as option (option.value)}
												<SelectItem value={option.value}>{option.label}</SelectItem>
											{/each}
										</SelectContent>
									</Select>
								</td>
								<td class="px-4 py-3 text-muted-foreground">
									{formatTimestamp(member.voucherRedeemedAt)}
								</td>
								<td class="px-4 py-3 text-muted-foreground">
									{formatTimestamp(member.voucherExpiresAt)}
								</td>
							</tr>
						{/each}
					{/if}
				</tbody>
			</table>
		</CardContent>
	</Card>

	<Card class="border-border/60">
		<CardHeader class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
			<div>
				<CardTitle>Enrollment tokens</CardTitle>
				<CardDescription>
					Generate one-time or multi-use tokens for new client deployments.
				</CardDescription>
			</div>
			<Button size="sm" class="gap-2" onclick={createToken}>
				<ShieldCheck class="h-4 w-4" />
				Generate token
			</Button>
		</CardHeader>
		<CardContent class="overflow-x-auto">
			<table class="min-w-full divide-y divide-border text-sm">
				<thead class="bg-muted/40 text-xs tracking-wide text-muted-foreground uppercase">
					<tr>
						<th class="px-4 py-2 text-left">Token</th>
						<th class="px-4 py-2 text-left">Created</th>
						<th class="px-4 py-2 text-left">Uses</th>
						<th class="px-4 py-2 text-left">Status</th>
						<th class="px-4 py-2 text-right">Actions</th>
					</tr>
				</thead>
				<tbody class="divide-y divide-border">
					{#if enrollmentTokens.length === 0}
						<tr>
							<td colspan="5" class="px-4 py-6 text-center text-muted-foreground">
								No enrollment tokens have been generated yet.
							</td>
						</tr>
					{:else}
						{#each enrollmentTokens as t (t.token)}
							{@const isExpired = t.expiresAt && new Date(t.expiresAt) < new Date()}
							{@const isExhausted = t.uses >= t.maxUses}
							{@const isRevoked = !!t.revokedAt}
							{@const isActive = !isRevoked && !isExpired && !isExhausted}
							<tr>
								<td class="px-4 py-3 font-mono text-xs max-w-[200px] truncate" title={t.token}>
									{t.token}
								</td>
								<td class="px-4 py-3 text-muted-foreground text-xs">
									{formatTimestamp(t.createdAt)}
								</td>
								<td class="px-4 py-3 text-xs">
									{t.uses} / {t.maxUses}
								</td>
								<td class="px-4 py-3">
									{#if isRevoked}
										<Badge variant="destructive" class="text-[10px]">Revoked</Badge>
									{:else if isExhausted}
										<Badge variant="outline" class="text-[10px]">Exhausted</Badge>
									{:else if isExpired}
										<Badge variant="outline" class="text-[10px]">Expired</Badge>
									{:else}
										<Badge variant="secondary" class="bg-emerald-500/10 text-emerald-600 text-[10px]"
											>Active</Badge
										>
									{/if}
								</td>
								<td class="px-4 py-3 text-right">
									{#if isActive}
										<Button
											variant="ghost"
											size="icon"
											class="h-8 w-8 text-rose-500"
											onclick={() => revokeToken(t.token)}
										>
											<TriangleAlert class="h-4 w-4" />
										</Button>
									{/if}
								</td>
							</tr>
						{/each}
					{/if}
				</tbody>
			</table>
		</CardContent>
	</Card>
	{#if hasChanges}
		<Card class="border border-amber-500/40 bg-amber-500/5">
			<CardHeader class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
				<div class="flex items-center gap-3">
					<Badge variant="outline" class="border-amber-500/40 bg-amber-500/10 text-amber-600"
						>Unsaved changes</Badge
					>
					<p class="text-sm text-muted-foreground">
						Save updates to propagate them to all operator consoles.
					</p>
				</div>
				<div class="flex items-center gap-2 text-xs tracking-wide text-muted-foreground uppercase">
					<Clock class="h-4 w-4" />
					Last saved: {lastSavedLabel}
				</div>
			</CardHeader>
			<CardFooter class="flex flex-wrap items-center justify-end gap-2">
				<Button type="button" variant="outline" size="sm" class="gap-2" onclick={restoreDefaults}>
					<TriangleAlert class="h-4 w-4" />
					Restore defaults
				</Button>
				<Button type="button" size="sm" class="gap-2" onclick={saveChanges}>
					<Check class="h-4 w-4" />
					Save all changes
				</Button>
			</CardFooter>
		</Card>
	{/if}

	<Card class="border-border/60">
		<CardHeader class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
			<div>
				<CardTitle class="text-base font-semibold">General console settings</CardTitle>
				<CardDescription>Branding and maintenance windows for this controller.</CardDescription>
			</div>
			{#if generalDirty}
				<Badge variant="secondary" class="bg-amber-500/20 text-amber-600">Unsaved</Badge>
			{/if}
		</CardHeader>
		<CardContent class="space-y-6">
			<div class="grid gap-4 md:grid-cols-2">
				<div class="space-y-2">
					<Label for="organization-name">Organization name</Label>
					<Input id="organization-name" bind:value={generalOrganizationName} />
					<p class="text-xs text-muted-foreground">
						Displayed to invited operators and in automation signatures.
					</p>
				</div>
				<div class="space-y-2">
					<Label for="control-plane-host">Control plane host</Label>
					<Input id="control-plane-host" bind:value={generalControlPlaneHost} />
					<p class="text-xs text-muted-foreground">
						Primary endpoint used for clients to establish uplinks.
					</p>
				</div>
				<div class="space-y-2">
					<Label for="maintenance-window">Maintenance window</Label>
					<Input id="maintenance-window" bind:value={generalMaintenanceWindow} />
					<p class="text-xs text-muted-foreground">
						Communicated to operators before scheduled downtime.
					</p>
				</div>
			</div>
			<Separator />
			<div class="grid gap-4 md:grid-cols-2">
				<div class="flex items-center justify-between rounded-md border border-border/60 px-3 py-2">
					<div class="space-y-1">
						<p class="text-sm leading-tight font-medium">Automatic controller updates</p>
						<p class="text-xs leading-tight text-muted-foreground">
							Applies security hotfixes without manual approval.
						</p>
					</div>
					<Switch bind:checked={generalAutoUpdate} aria-label="Toggle automatic updates" />
				</div>
				<div class="flex items-center justify-between rounded-md border border-border/60 px-3 py-2">
					<div class="space-y-1">
						<p class="text-sm leading-tight font-medium">Allow beta channels</p>
						<p class="text-xs leading-tight text-muted-foreground">
							Opt-in to preview builds for early capability access.
						</p>
					</div>
					<Switch bind:checked={generalAllowBeta} aria-label="Toggle beta channel access" />
				</div>
			</div>
		</CardContent>
		<CardFooter class="flex flex-wrap items-center justify-between gap-3">
			<p class="text-xs text-muted-foreground">
				Changes replicate to all connected consoles after saving.
			</p>
			<Button
				type="button"
				variant="outline"
				size="sm"
				onclick={() => resetSection('general')}
				disabled={!generalDirty}
			>
				Revert section
			</Button>
		</CardFooter>
	</Card>

	<Card class="border-border/60">
		<CardHeader class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
			<div>
				<CardTitle class="text-base font-semibold">Notifications</CardTitle>
				<CardDescription>Control the cadence of alerts across channels.</CardDescription>
			</div>
			{#if notificationsDirty}
				<Badge variant="secondary" class="bg-amber-500/20 text-amber-600">Unsaved</Badge>
			{/if}
		</CardHeader>
		<CardContent class="space-y-6">
			<div class="space-y-4">
				<p class="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
					Digest frequency
				</p>
				<div class="flex flex-wrap gap-2">
					{#each digestOptions as option (option.value)}
						<Button
							type="button"
							size="sm"
							variant={notificationsDigestFrequency === option.value ? 'default' : 'outline'}
							onclick={() => (notificationsDigestFrequency = option.value)}
						>
							{option.label}
						</Button>
					{/each}
				</div>
			</div>
			<Separator />
			<div class="grid gap-4 md:grid-cols-2">
				<div class="flex items-center justify-between rounded-md border border-border/60 px-3 py-2">
					<div class="space-y-1">
						<p class="text-sm leading-tight font-medium">Real-time operations alerts</p>
						<p class="text-xs leading-tight text-muted-foreground">
							Send notifications for new tasks, pivots, and command results.
						</p>
					</div>
					<Switch bind:checked={notificationsRealtimeOps} aria-label="Toggle real-time alerts" />
				</div>
				<div class="flex items-center justify-between rounded-md border border-border/60 px-3 py-2">
					<div class="space-y-1">
						<p class="text-sm leading-tight font-medium">Escalate critical alerts</p>
						<p class="text-xs leading-tight text-muted-foreground">
							Escalate high-risk detections to the incident bridge.
						</p>
					</div>
					<Switch
						bind:checked={notificationsEscalateCritical}
						aria-label="Toggle critical escalations"
					/>
				</div>
				<div class="flex items-center justify-between rounded-md border border-border/60 px-3 py-2">
					<div class="space-y-1">
						<p class="text-sm leading-tight font-medium">Email bridge</p>
						<p class="text-xs leading-tight text-muted-foreground">
							Deliver summaries to the operations distribution list.
						</p>
					</div>
					<Switch bind:checked={notificationsEmailBridge} aria-label="Toggle email bridge" />
				</div>
				<div class="flex items-center justify-between rounded-md border border-border/60 px-3 py-2">
					<div class="space-y-1">
						<p class="text-sm leading-tight font-medium">Slack bridge</p>
						<p class="text-xs leading-tight text-muted-foreground">
							Mirror alerts to the #ops-war-room channel.
						</p>
					</div>
					<Switch bind:checked={notificationsSlackBridge} aria-label="Toggle slack bridge" />
				</div>
			</div>
		</CardContent>
		<CardFooter class="flex flex-wrap items-center justify-between gap-3">
			<p class="text-xs text-muted-foreground">
				Adjust channel mix to keep operators aligned with mission tempo.
			</p>
			<Button
				type="button"
				variant="outline"
				size="sm"
				onclick={() => resetSection('notifications')}
				disabled={!notificationsDirty}
			>
				Revert section
			</Button>
		</CardFooter>
	</Card>

	<Card class="border-border/60">
		<CardHeader class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
			<div>
				<CardTitle class="text-base font-semibold">Security & access control</CardTitle>
				<CardDescription>Harden operator access and review workflows.</CardDescription>
			</div>
			{#if securityDirty}
				<Badge variant="secondary" class="bg-amber-500/20 text-amber-600">Unsaved</Badge>
			{/if}
		</CardHeader>
		<CardContent class="space-y-6">
			<div class="grid gap-4 md:grid-cols-2">
				<div class="space-y-2">
					<Label for="session-timeout">Session timeout (minutes)</Label>
					<Input
						id="session-timeout"
						type="number"
						min="5"
						bind:value={securitySessionTimeoutMinutes}
					/>
					<p class="text-xs text-muted-foreground">
						Force operators to re-authenticate after periods of inactivity.
					</p>
				</div>
				<div class="space-y-2">
					<Label for="command-quorum">Command approval quorum</Label>
					<Input id="command-quorum" type="number" min="1" bind:value={securityCommandQuorum} />
					<p class="text-xs text-muted-foreground">
						Minimum approvers required for destructive queued tasks.
					</p>
				</div>
			</div>
			<Separator />
			<div class="grid gap-4 md:grid-cols-2">
				<div class="flex items-center justify-between rounded-md border border-border/60 px-3 py-2">
					<div class="space-y-1">
						<p class="text-sm leading-tight font-medium">Require multi-factor authentication</p>
						<p class="text-xs leading-tight text-muted-foreground">
							Enforce MFA on every operator login.
						</p>
					</div>
					<Switch bind:checked={securityEnforceMfa} aria-label="Toggle MFA requirement" />
				</div>
				<div class="flex items-center justify-between rounded-md border border-border/60 px-3 py-2">
					<div class="space-y-1">
						<p class="text-sm leading-tight font-medium">IP allowlist enforcement</p>
						<p class="text-xs leading-tight text-muted-foreground">
							Restrict console logins to the trusted operations network.
						</p>
					</div>
					<Switch bind:checked={securityIpAllowlist} aria-label="Toggle IP allowlist" />
				</div>
				<div class="flex items-center justify-between rounded-md border border-border/60 px-3 py-2">
					<div class="space-y-1">
						<p class="text-sm leading-tight font-medium">Require runbook approval</p>
						<p class="text-xs leading-tight text-muted-foreground">
							Route high-impact automations through approval workflow.
						</p>
					</div>
					<Switch bind:checked={securityRequireApproval} aria-label="Toggle command approval" />
				</div>
			</div>
		</CardContent>
		<CardFooter class="flex flex-wrap items-center justify-between gap-3">
			<div class="flex items-center gap-2 text-xs tracking-wide text-muted-foreground uppercase">
				<ShieldCheck class="h-4 w-4" />
				Policy updates take effect immediately after saving.
			</div>
			<Button
				type="button"
				variant="outline"
				size="sm"
				onclick={() => resetSection('security')}
				disabled={!securityDirty}
			>
				Revert section
			</Button>
		</CardFooter>
	</Card>

	<div
		class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/30 px-4 py-3 text-sm"
	>
		<div class="flex items-center gap-3 text-muted-foreground">
			<UserPlus class="h-4 w-4" />
			Invite additional operators once security policies are confirmed.
		</div>
		<div class="flex items-center gap-2">
			<Button type="button" variant="ghost" size="sm" class="gap-2" onclick={restoreDefaults}>
				<TriangleAlert class="h-4 w-4" />
				Restore defaults
			</Button>
			<Button type="button" size="sm" class="gap-2" onclick={saveChanges} disabled={!hasChanges}>
				<Check class="h-4 w-4" />
				Save changes
			</Button>
		</div>
	</div>
</section>
