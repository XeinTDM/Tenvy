import type { CommandResult } from "./messages";

export type AgentStatus = "online" | "offline" | "error";

export interface AgentLocation {
  source?: string;
  city?: string;
  region?: string;
  country?: string;
  countryCode?: string;
}

export interface AgentMetadata {
  hostname: string;
  username: string;
  os: string;
  architecture: string;
  ipAddress?: string;
  publicIpAddress?: string;
  tags?: string[];
  version?: string;
  group?: string;
  location?: AgentLocation;
  hardwareId?: string;
}

export interface AgentMetrics {
  memoryBytes?: number;
  goroutines?: number;
  uptimeSeconds?: number;
  pingMs?: number;
  latencyMs?: number;
}

export interface AgentOperatorNote {
  note: string;
  tags: string[];
  updatedAt: string | null;
  updatedBy?: string | null;
}

export interface AgentSnapshot {
  id: string;
  metadata: AgentMetadata;
  status: AgentStatus;
  connectedAt: string;
  lastSeen: string;
  metrics?: AgentMetrics;
  pendingCommands: number;
  recentResults: CommandResult[];
  liveSession?: boolean;
  operatorNote?: AgentOperatorNote;
}

export interface AgentListResponse {
  agents: AgentSnapshot[];
}

export interface AgentDetailResponse {
  agent: AgentSnapshot;
}

export type AgentConnectionAction = "disconnect" | "reconnect";

export interface AgentConnectionRequest {
  action: AgentConnectionAction;
}

export interface AgentConnectionResponse {
  agent: AgentSnapshot;
}

export interface AgentTagUpdateRequest {
  tags: string[];
}

export interface AgentTagUpdateResponse {
  agent: AgentSnapshot;
}
