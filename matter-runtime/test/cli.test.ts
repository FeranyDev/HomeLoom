import assert from "node:assert/strict";
import { test } from "node:test";
import { IPC_PROTOCOL_VERSION } from "../src/contract.js";
import { parseArguments, readyEvent } from "../src/cli.js";

test("CLI defaults to the real matter.js adapter and keeps fake explicit", () => {
  const previous = process.env.HOMELOOM_MATTER_ADAPTER;
  delete process.env.HOMELOOM_MATTER_ADAPTER;
  try {
    assert.equal(
      parseArguments(["--socket", "/tmp/matter.sock", "--target", "matter-a"]).adapter,
      "matter-js",
    );
    assert.equal(
      parseArguments([
        "--socket",
        "/tmp/matter.sock",
        "--target",
        "matter-a",
        "--adapter",
        "fake",
      ]).adapter,
      "fake",
    );
  } finally {
    if (previous === undefined) {
      delete process.env.HOMELOOM_MATTER_ADAPTER;
    } else {
      process.env.HOMELOOM_MATTER_ADAPTER = previous;
    }
  }
});

test("CLI ready event advertises the current IPC contract version", () => {
  const options = parseArguments([
    "--socket",
    "/tmp/matter-camera.sock",
    "--target",
    "matter-camera",
    "--adapter",
    "fake",
  ]);

  assert.equal(readyEvent(options).protocolVersion, IPC_PROTOCOL_VERSION);
  assert.equal(readyEvent(options).protocolVersion, "1.1");
});
