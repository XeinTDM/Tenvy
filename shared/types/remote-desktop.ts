export type RemoteDesktopQuality = "auto" | "high" | "medium" | "low";

export type RemoteDesktopStreamMode = "images" | "video";

export type RemoteDesktopEncoder = "auto" | "hevc" | "avc" | "jpeg";

export type RemoteDesktopTransport = "http" | "webrtc";

export type RemoteDesktopHardwarePreference = "auto" | "prefer" | "avoid";

export interface RemoteDesktopMediaSample {
  kind: "video" | "audio";
  codec: RemoteDesktopEncoder | "opus" | "pcm";
  timestamp: number;
  keyFrame?: boolean;
  format?: "annexb" | "opus" | "pcm" | "aac" | "jpeg";
  data: string | Uint8Array;
}

export interface RemoteDesktopTransportDiagnostics {
  transport: RemoteDesktopTransport;
  codec?: RemoteDesktopEncoder;
  bandwidthEstimateKbps?: number;
  availableBitrateKbps?: number;
  currentBitrateKbps?: number;
  rttMs?: number;
  jitterMs?: number;
  packetsLost?: number;
  framesDropped?: number;
  lastUpdatedAt: string;
  hardwareFallback?: boolean;
  hardwareEncoder?: string;
}

export interface RemoteDesktopWebRTCICEServer {
  urls: string[];
  username?: string;
  credential?: string;
  credentialType?: "password" | "oauth";
}

export interface RemoteDesktopTransportCapability {
  transport: RemoteDesktopTransport;
  codecs: RemoteDesktopEncoder[];
  features?: {
    intraRefresh?: boolean;
    binaryFrames?: boolean;
  };
}

export interface RemoteDesktopInputQuicConfig {
  enabled: boolean;
  port?: number;
  alpn?: string;
}

export interface RemoteDesktopInputNegotiation {
  quic?: RemoteDesktopInputQuicConfig;
}

export interface RemoteDesktopSessionNegotiationRequest {
  sessionId: string;
  transports: RemoteDesktopTransportCapability[];
  codecs?: RemoteDesktopEncoder[];
  intraRefresh?: boolean;
  pluginVersion?: string;
  webrtc?: {
    offer?: string;
    dataChannel?: string;
    iceServers?: RemoteDesktopWebRTCICEServer[];
  };
}

export interface RemoteDesktopSessionNegotiationResponse {
  accepted: boolean;
  transport?: RemoteDesktopTransport;
  codec?: RemoteDesktopEncoder;
  intraRefresh?: boolean;
  features?: {
    binaryFrames?: boolean;
  };
  reason?: string;
  requiredPluginVersion?: string;
  webrtc?: {
    answer?: string;
    dataChannel?: string;
    iceServers?: RemoteDesktopWebRTCICEServer[];
  };
  input?: RemoteDesktopInputNegotiation;
}

export interface RemoteDesktopSettings {
  quality: RemoteDesktopQuality;
  monitor: number;
  mouse: boolean;
  keyboard: boolean;
  mode: RemoteDesktopStreamMode;
  encoder?: RemoteDesktopEncoder;
  transport?: RemoteDesktopTransport;
  hardware?: RemoteDesktopHardwarePreference;
  targetBitrateKbps?: number;
}

export interface RemoteDesktopSettingsPatch {
  quality?: RemoteDesktopQuality;
  monitor?: number;
  mouse?: boolean;
  keyboard?: boolean;
  mode?: RemoteDesktopStreamMode;
  encoder?: RemoteDesktopEncoder;
  transport?: RemoteDesktopTransport;
  hardware?: RemoteDesktopHardwarePreference;
  targetBitrateKbps?: number;
}

export type RemoteDesktopCommandAction =
  | "start"
  | "stop"
  | "configure"
  | "input";

export type RemoteDesktopMouseButton = "left" | "middle" | "right";

export interface RemoteDesktopInputEventBase {
  capturedAt: number;
}

export type RemoteDesktopInputEvent =
  | (RemoteDesktopInputEventBase & {
      type: "mouse-move";
      x: number;
      y: number;
      normalized?: boolean;
      monitor?: number;
    })
  | (RemoteDesktopInputEventBase & {
      type: "mouse-button";
      button: RemoteDesktopMouseButton;
      pressed: boolean;
      monitor?: number;
    })
  | (RemoteDesktopInputEventBase & {
      type: "mouse-scroll";
      deltaX: number;
      deltaY: number;
      deltaMode?: number;
      monitor?: number;
    })
  | (RemoteDesktopInputEventBase & {
      type: "key";
      pressed: boolean;
      key?: string;
      code?: string;
      keyCode?: number;
      repeat?: boolean;
      altKey?: boolean;
      ctrlKey?: boolean;
      shiftKey?: boolean;
      metaKey?: boolean;
    });

export interface RemoteDesktopCommandPayload {
  action: RemoteDesktopCommandAction;
  sessionId?: string;
  settings?: RemoteDesktopSettingsPatch;
  events?: RemoteDesktopInputEvent[];
}

export interface RemoteDesktopInputBurst {
  sessionId: string;
  events: RemoteDesktopInputEvent[];
  sequence?: number;
}

export interface RemoteDesktopFrameMetrics {
  fps?: number;
  bandwidthKbps?: number;
  captureLatencyMs?: number;
  encodeLatencyMs?: number;
  processingLatencyMs?: number;
  frameJitterMs?: number;
  targetBitrateKbps?: number;
  ladderLevel?: number;
  frameLossPercent?: number;
}

export interface RemoteDesktopCursorState {
  x: number;
  y: number;
  visible: boolean;
}

export interface RemoteDesktopDeltaRect {
  x: number;
  y: number;
  width: number;
  height: number;
  encoding: "png" | "jpeg";
  data: string | Uint8Array;
}

export interface RemoteDesktopVideoFrame {
  offsetMs: number;
  width: number;
  height: number;
  encoding: "jpeg";
  data: string | Uint8Array;
}

export interface RemoteDesktopVideoClip {
  durationMs: number;
  frames: RemoteDesktopVideoFrame[];
}

export interface RemoteDesktopFramePacket {
  sessionId: string;
  sequence: number;
  timestamp: string;
  width: number;
  height: number;
  keyFrame: boolean;
  encoding: "png" | "jpeg" | "clip";
  transport?: RemoteDesktopTransport;
  image?: string | Uint8Array;
  deltas?: RemoteDesktopDeltaRect[];
  clip?: RemoteDesktopVideoClip;
  encoder?: RemoteDesktopEncoder;
  encoderHardware?: string;
  intraRefresh?: boolean;
  monitors?: RemoteDesktopMonitor[];
  cursor?: RemoteDesktopCursorState;
  metrics?: RemoteDesktopFrameMetrics;
  media?: RemoteDesktopMediaSample[];
}

export interface RemoteDesktopMonitor {
  id: number;
  label: string;
  width: number;
  height: number;
}

export interface RemoteDesktopSessionState {
  sessionId: string;
  agentId: string;
  active: boolean;
  createdAt: string;
  lastUpdatedAt?: string;
  lastSequence?: number;
  settings: RemoteDesktopSettings;
  activeEncoder?: RemoteDesktopEncoder;
  negotiatedTransport?: RemoteDesktopTransport;
  negotiatedCodec?: RemoteDesktopEncoder;
  intraRefresh?: boolean;
  encoderHardware?: string;
  monitors: RemoteDesktopMonitor[];
  metrics?: RemoteDesktopFrameMetrics;
  transportDiagnostics?: RemoteDesktopTransportDiagnostics;
}

export interface RemoteDesktopSessionResponse {
  session: RemoteDesktopSessionState | null;
}

export interface RemoteDesktopStreamSessionMessage {
  session: RemoteDesktopSessionState;
}

export interface RemoteDesktopStreamFrameMessage {
  frame: RemoteDesktopFramePacket;
}

export interface RemoteDesktopStreamMediaMessage {
  sessionId: string;
  media: RemoteDesktopMediaSample[];
}

export interface RemoteDesktopStreamEndMessage {
  reason?: string;
}
