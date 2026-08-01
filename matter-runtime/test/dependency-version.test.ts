import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";

test("pins all direct matter.js packages to the approved stable release", async () => {
  const manifest = JSON.parse(
    await readFile(new URL("../../package.json", import.meta.url), "utf8"),
  ) as { dependencies?: Record<string, string> };

  assert.equal(manifest.dependencies?.["@matter/general"], "0.17.7");
  assert.equal(manifest.dependencies?.["@matter/main"], "0.17.7");
});
