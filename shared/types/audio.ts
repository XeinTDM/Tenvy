export type AudioDirection = "input" | "output";

export type AudioStreamEncoding = "pcm16";

export type AudioStreamTransportType = "http-chunk" | "websocket";

export interface AudioStreamTransport {
  transport: AudioStreamTransportType;
  url: string;
  protocol?: string;
  headers?: Record<string, string>;
}

export interface AudioDeviceDescriptor {
  id: string;
  deviceId: string;
  label: string;
  kind: AudioDirection;
  groupId?: string;
  systemDefault?: boolean;
  communicationsDefault?: boolean;
  lastSeen: string;
}

export interface AudioDeviceInventory {
  inputs: AudioDeviceDescriptor[];
  outputs: AudioDeviceDescriptor[];
  capturedAt: string;
  requestId?: string;
}

export interface AudioDeviceInventoryState {
  inventory?: AudioDeviceInventory | null;
  pending?: boolean;
}

export interface AudioDeviceRefreshResponse {
  requestId: string;
}

export interface AudioStreamFormat {
  encoding: AudioStreamEncoding;
  sampleRate: number;
  channels: number;
}

export interface AudioStreamChunk {
  sessionId: string;
  sequence: number;
  timestamp: string;
  format: AudioStreamFormat;
  data: string | Uint8Array;
}

export interface AudioSessionState {
  sessionId: string;
  agentId: string;
  deviceId?: string;
  deviceLabel?: string;
  direction: AudioDirection;
  format: AudioStreamFormat;
  startedAt: string;
  lastUpdatedAt?: string;
  active: boolean;
  lastSequence?: number;
  transport?: AudioStreamTransport;
}

export interface AudioSessionResponse {
  session: AudioSessionState | null;
}

export interface AudioSessionRequest {
  deviceId?: string;
  deviceLabel?: string;
  direction?: AudioDirection;
  sampleRate?: number;
  channels?: number;
  encoding?: AudioStreamEncoding;
}

export type AudioControlAction =
  | "enumerate"
  | "inventory"
  | "start"
  | "stop"
  | "playback-start"
  | "playback-pause"
  | "playback-resume"
  | "playback-stop";

export interface AudioControlCommandPayload {
  action: AudioControlAction;
  requestId?: string;
  sessionId?: string;
  deviceId?: string;
  deviceLabel?: string;
  direction?: AudioDirection;
  sampleRate?: number;
  channels?: number;
  encoding?: AudioStreamEncoding;
  streamTransport?: AudioStreamTransport;
  trackId?: string;
  trackUrl?: string;
  outputDeviceId?: string;
  outputDeviceLabel?: string;
  volume?: number;
  loop?: boolean;
  chaosMode?: boolean;
  rickroll?: boolean;
}

export interface AudioUploadTrack {
  id: string;
  filename: string;
  originalName: string;
  size: number;
  contentType?: string;
  uploadedAt: string;
  downloadUrl: string;
}

export interface AudioUploadResponse {
  uploads: AudioUploadTrack[];
}
