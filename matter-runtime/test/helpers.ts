import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { BridgeConfiguration, DeviceSnapshot } from "../src/contract.js";

export async function temporarySocket(prefix: string): Promise<{
  directory: string;
  socketPath: string;
  cleanup(): Promise<void>;
}> {
  const directory = await mkdtemp(join(tmpdir(), `homeloom-${prefix}-`));
  return {
    directory,
    socketPath: join(directory, "runtime.sock"),
    cleanup: () => rm(directory, { recursive: true, force: true }),
  };
}

export function bridgeConfiguration(
  targetId: string,
  overrides: Partial<BridgeConfiguration> = {},
): BridgeConfiguration {
  return {
    nodeKind: "bridge",
    targetId,
    identityNamespace: targetId,
    networkInterface: "en0",
    listenPort: 5_540,
    discriminator: 1_234,
    commissioningPasscode: 20_202_021,
    vendorId: 0xfff1,
    productId: 0x8000,
    productName: `HomeLoom ${targetId}`,
    serialNumber: `serial-${targetId}`,
    commissioningWindowSeconds: 900,
    ...overrides,
  };
}

export function switchDevice(
  id: string,
  endpointId = 2,
  on = false,
): DeviceSnapshot {
  return {
    id,
    endpointId,
    deviceType: "OnOffLight",
    name: `Switch ${id}`,
    reachable: true,
    attributes: {
      "OnOff.OnOff": on,
    },
  };
}

export async function eventually(
  predicate: () => boolean,
  timeoutMs = 2_000,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!predicate()) {
    if (Date.now() >= deadline) {
      throw new Error(`condition was not met within ${timeoutMs}ms`);
    }
    await new Promise<void>((resolve) => setTimeout(resolve, 5));
  }
}
