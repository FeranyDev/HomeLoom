import { build } from "esbuild";
import { chmod, mkdir, readFile, stat, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawn } from "node:child_process";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const runtimeDirectory = resolve(scriptDirectory, "..");
const repositoryDirectory = resolve(runtimeDirectory, "..");
const bundleDirectory = resolve(repositoryDirectory, ".cache/matter-runtime/sea");
const bundlePath = resolve(bundleDirectory, "matter-runtime.mjs");
const defaultOutput = resolve(repositoryDirectory, "backend/bin/homeloom-matter-runtime");
const options = parseOptions(process.argv.slice(2));
const seaBuilderNode =
  options.builderNode ||
  process.env.HOMELOOM_SEA_BUILDER_NODE?.trim() ||
  process.env.HOMELOOM_SEA_NODE?.trim() ||
  process.execPath;
const seaTargetNode =
  options.targetNode ||
  process.env.HOMELOOM_SEA_TARGET_NODE?.trim() ||
  process.env.HOMELOOM_SEA_NODE?.trim() ||
  seaBuilderNode;
const targetPlatform = normalizePlatform(
  options.targetPlatform || process.env.HOMELOOM_SEA_TARGET_PLATFORM || process.platform,
);

const outputPath = options.output || defaultOutput;
const builderVersionText = await readNodeVersion(seaBuilderNode);
assertSeaNodeVersion(builderVersionText, seaBuilderNode);
const targetVersionText =
  options.targetNodeVersion ||
  process.env.HOMELOOM_SEA_TARGET_NODE_VERSION?.trim() ||
  builderVersionText;
assertSameNodeVersion(builderVersionText, targetVersionText, seaBuilderNode, seaTargetNode);
await assertNodeBinary(seaTargetNode);

await mkdir(bundleDirectory, { recursive: true });
await mkdir(dirname(outputPath), { recursive: true });

await build({
  entryPoints: [resolve(runtimeDirectory, "dist/src/cli.js")],
  bundle: true,
  format: "esm",
  platform: "node",
  target: "node20",
  outfile: bundlePath,
  sourcemap: false,
  plugins: [bunSqliteStubPlugin()],
});

const bundle = await readFile(bundlePath, "utf8");
if (bundle.includes('from "@matter/') || bundle.includes("from '@matter/")) {
  throw new Error("Matter SEA bundle still contains an external @matter import");
}

const configPath = resolve(bundleDirectory, "sea-config.json");
await writeFile(
  configPath,
  `${JSON.stringify(
    {
      main: bundlePath,
      mainFormat: "module",
      executable: seaTargetNode,
      output: outputPath,
      disableExperimentalSEAWarning: true,
      useSnapshot: false,
      useCodeCache: false,
      execArgvExtension: "env",
    },
    null,
    2,
  )}\n`,
  "utf8",
);

await run(seaBuilderNode, ["--build-sea", configPath]);
await chmod(outputPath, 0o755);
if (targetPlatform === "darwin" && !options.skipSign) {
  if (process.platform !== "darwin") {
    throw new Error(
      "cross-built macOS SEA must be signed on a macOS builder; pass --skip-sign to defer signing",
    );
  }
  const identity = process.env.HOMELOOM_CODESIGN_IDENTITY?.trim() || "-";
  await run("codesign", ["--force", "--sign", identity, outputPath]);
  await run("codesign", ["--verify", "--verbose", outputPath]);
}
console.log(
  `built Matter SEA runtime (${targetPlatform}) with ${seaTargetNode}: ${outputPath}`,
);

function parseOptions(arguments_) {
  const parsed = {
    builderNode: "",
    output: "",
    skipSign: false,
    targetNode: "",
    targetNodeVersion: "",
    targetPlatform: "",
  };
  for (let index = 0; index < arguments_.length; index += 1) {
    const argument = arguments_[index];
    if (argument === "--output") {
      parsed.output = requireArgument(arguments_, ++index, argument);
    } else if (argument?.startsWith("--output=")) {
      parsed.output = argument.slice("--output=".length);
    } else if (argument === "--builder-node") {
      parsed.builderNode = requireArgument(arguments_, ++index, argument);
    } else if (argument?.startsWith("--builder-node=")) {
      parsed.builderNode = argument.slice("--builder-node=".length);
    } else if (argument === "--target-node") {
      parsed.targetNode = requireArgument(arguments_, ++index, argument);
    } else if (argument?.startsWith("--target-node=")) {
      parsed.targetNode = argument.slice("--target-node=".length);
    } else if (argument === "--target-node-version") {
      parsed.targetNodeVersion = requireArgument(arguments_, ++index, argument);
    } else if (argument?.startsWith("--target-node-version=")) {
      parsed.targetNodeVersion = argument.slice("--target-node-version=".length);
    } else if (argument === "--target-platform") {
      parsed.targetPlatform = requireArgument(arguments_, ++index, argument);
    } else if (argument?.startsWith("--target-platform=")) {
      parsed.targetPlatform = argument.slice("--target-platform=".length);
    } else if (argument === "--skip-sign") {
      parsed.skipSign = true;
    } else if (argument !== undefined) {
      throw new Error(`unknown argument ${argument}`);
    }
  }
  if (parsed.output.trim() !== "") {
    parsed.output = isAbsolute(parsed.output)
      ? parsed.output
      : resolve(repositoryDirectory, parsed.output);
  }
  if (parsed.builderNode.trim() !== "") {
    parsed.builderNode = resolveIfPath(parsed.builderNode);
  }
  if (parsed.targetNode.trim() !== "") {
    parsed.targetNode = resolveIfPath(parsed.targetNode);
  }
  return parsed;
}

function requireArgument(arguments_, index, option) {
  const value = arguments_[index];
  if (value === undefined || value.trim() === "" || value.startsWith("--")) {
    throw new Error(`${option} requires a value`);
  }
  return value;
}

function resolveIfPath(value) {
  return value.includes("/") || value.includes("\\") ? resolve(value) : value;
}

function normalizePlatform(platform) {
  switch (platform.trim().toLowerCase()) {
    case "darwin":
    case "macos":
      return "darwin";
    case "linux":
      return "linux";
    case "win32":
    case "windows":
      return "win32";
    default:
      throw new Error(`unsupported SEA target platform: ${platform}`);
  }
}

function assertSeaNodeVersion(versionText, command) {
  const [major, minor] = versionText.split(".").map(Number);
  if (major < 25 || (major === 25 && minor < 5)) {
    throw new Error(
      `Node.js >=25.5 is required to build a SEA with --build-sea; found ${versionText} from ${command}`,
    );
  }
}

function assertSameNodeVersion(builderVersion, targetVersion, builderNode, targetNode) {
  if (builderVersion !== targetVersion) {
    throw new Error(
      `SEA builder and target Node versions must match; builder ${builderVersion} (${builderNode}), target ${targetVersion} (${targetNode})`,
    );
  }
}

async function assertNodeBinary(command) {
  if (!command.includes("/") && !command.includes("\\")) {
    return;
  }
  const info = await stat(command).catch(() => undefined);
  if (!info?.isFile()) {
    throw new Error(`SEA target Node binary does not exist: ${command}`);
  }
}

function bunSqliteStubPlugin() {
  return {
    name: "homeloom-unused-sqlite-stub",
    setup(pluginBuild) {
      pluginBuild.onResolve({ filter: /^(bun|node):sqlite$/ }, ({ path }) => ({
        path,
        namespace: "homeloom-unused-sqlite-stub",
      }));
      pluginBuild.onLoad(
        { filter: /^(bun|node):sqlite$/, namespace: "homeloom-unused-sqlite-stub" },
        () => ({
          loader: "js",
          contents: `
            export const constants = {};
            export class Database {
              constructor() {
                throw new Error("SQLite is not part of the HomeLoom Matter SEA runtime");
              }
            }
            export class DatabaseSync extends Database {}
          `,
        }),
      );
    },
  };
}

function run(command, arguments_) {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(command, arguments_, { stdio: "inherit" });
    child.once("error", reject);
    child.once("exit", (code, signal) => {
      if (code === 0) {
        resolvePromise();
        return;
      }
      reject(new Error(`SEA build failed with ${signal ?? `exit code ${code}`}`));
    });
  });
}

function readNodeVersion(command) {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(command, ["-p", "process.versions.node"], {
      stdio: ["ignore", "pipe", "inherit"],
    });
    let output = "";
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      output += chunk;
    });
    child.once("error", reject);
    child.once("close", (code, signal) => {
      if (code === 0 && output.trim() !== "") {
        resolvePromise(output.trim());
        return;
      }
      reject(
        new Error(
          `could not read Node.js version from ${command} (${signal ?? `exit code ${code}`})`,
        ),
      );
    });
  });
}
