import assert from "node:assert/strict";
import { test } from "node:test";
import { parseArguments } from "../src/cli.js";

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
