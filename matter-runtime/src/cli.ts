import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { FakeProtocolAdapter } from "./runtime/fake-adapter.js";
import { MatterJsAdapter } from "./runtime/matter-js-adapter.js";
import { RuntimeServer } from "./runtime/server.js";

interface CliOptions {
  socketPath: string;
  targetId: string;
  identityNamespace: string;
  adapter: "fake" | "matter-js";
  requestTimeoutMs: number;
  maxQueue: number;
}

export async function runCli(arguments_: string[]): Promise<void> {
  assertSupportedNode();
  const options = parseArguments(arguments_);
  let requestStop: (() => void) | undefined;
  const adapter = options.adapter === "fake"
    ? new FakeProtocolAdapter()
    : new MatterJsAdapter();
  const server = new RuntimeServer(adapter, {
    socketPath: options.socketPath,
    targetId: options.targetId,
    identityNamespace: options.identityNamespace,
    requestTimeoutMs: options.requestTimeoutMs,
    maxInboundQueue: options.maxQueue,
    maxOutboundQueue: options.maxQueue,
    onFactoryResetComplete: () => requestStop?.(),
  });
  await server.start();
  process.stdout.write(`${JSON.stringify({
    event: "ready",
    protocolVersion: "1.0",
    targetId: options.targetId,
    identityNamespace: options.identityNamespace,
    socketPath: options.socketPath,
    adapter: options.adapter,
  })}\n`);

  await new Promise<void>((resolve, reject) => {
    let stopping = false;
    const stop = (): void => {
      if (stopping) {
        return;
      }
      stopping = true;
      void server.stop().then(resolve, reject);
    };
    requestStop = stop;
    process.once("SIGINT", stop);
    process.once("SIGTERM", stop);
  });
}

export function parseArguments(arguments_: string[]): CliOptions {
  const values = new Map<string, string>();
  for (let index = 0; index < arguments_.length; index += 1) {
    const argument = arguments_[index];
    if (argument === undefined) {
      continue;
    }
    if (argument === "--help" || argument === "-h") {
      throw new Error(
        "usage: node dist/src/cli.js --socket <path> --target <id> " +
        "[--identity-namespace <name>] [--adapter fake|matter-js] [--max-queue <n>]",
      );
    }
    if (!argument.startsWith("--")) {
      throw new Error(`unexpected argument ${argument}`);
    }
    const separator = argument.indexOf("=");
    const rawName = separator < 0 ? argument : argument.slice(0, separator);
    const name = rawName === "--target-id" ? "--target" : rawName;
    const inlineValue = separator < 0 ? undefined : argument.slice(separator + 1);
    const value = inlineValue ?? arguments_[++index];
    if (value === undefined || value.startsWith("--")) {
      throw new Error(`${name} requires a value`);
    }
    if (!["--socket", "--target", "--identity-namespace", "--adapter", "--request-timeout-ms", "--max-queue"].includes(name)) {
      throw new Error(`unknown option ${name}`);
    }
    values.set(name, value);
  }

  const socketPath = required(values, "--socket");
  const targetId = required(values, "--target");
  const identityNamespace = values.get("--identity-namespace") ?? targetId;
  const adapterName = values.get("--adapter") ?? process.env.HOMELOOM_MATTER_ADAPTER ?? "matter-js";
  if (adapterName !== "fake" && adapterName !== "matter-js") {
    throw new Error("--adapter must be fake or matter-js");
  }
  return {
    socketPath,
    targetId,
    identityNamespace,
    adapter: adapterName,
    requestTimeoutMs: positiveInteger(values.get("--request-timeout-ms") ?? "5000", "--request-timeout-ms"),
    maxQueue: positiveInteger(values.get("--max-queue") ?? "128", "--max-queue"),
  };
}

function required(values: Map<string, string>, name: string): string {
  const value = values.get(name);
  if (value === undefined || value.trim() === "") {
    throw new Error(`${name} is required`);
  }
  return value;
}

function positiveInteger(value: string, name: string): number {
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < 1) {
    throw new Error(`${name} must be a positive integer`);
  }
  return parsed;
}

function assertSupportedNode(): void {
  const major = Number(process.versions.node.split(".")[0]);
  if (!Number.isSafeInteger(major) || major < 20) {
    throw new Error(`Node.js >=20 is required; found ${process.versions.node}`);
  }
}

if (
  process.argv[1] !== undefined &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  void runCli(process.argv.slice(2)).catch((error: unknown) => {
    const message = error instanceof Error ? error.stack ?? error.message : String(error);
    process.stderr.write(`${message}\n`);
    process.exitCode = 1;
  });
}
