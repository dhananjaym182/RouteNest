// Run with: node --test tools/gen-provider-presets.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const generator = path.join(here, "gen-provider-presets.mjs");
const fixture = path.join(here, "testdata", "provider-registry-fixture");

function runGenerator(out) {
  execFileSync(process.execPath, [
    generator,
    path.join(fixture, "registry"),
    out,
    path.join(fixture, "overrides.json"),
    path.join(fixture, "manual"),
  ], { stdio: ["ignore", "ignore", "pipe"] });
  return fs.readFileSync(out);
}

function tempFile(name) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "gen-presets-"));
  return path.join(dir, name);
}

test("output matches the checked-in fixture byte for byte", () => {
  const got = runGenerator(tempFile("imported.json"));
  const want = fs.readFileSync(path.join(fixture, "expected-imported.json"));
  assert.equal(got.toString("utf8"), want.toString("utf8"));
});

test("two runs produce identical bytes", () => {
  const a = runGenerator(tempFile("a.json"));
  const b = runGenerator(tempFile("b.json"));
  assert.ok(a.equals(b), "generator output is not deterministic");
});

test("registry directory is required", () => {
  const res = spawnSync(process.execPath, [generator], { encoding: "utf8" });
  assert.equal(res.status, 2);
  assert.match(res.stderr, /usage:/);
});

test("generated ids are sorted by code unit", () => {
  const obj = JSON.parse(runGenerator(tempFile("order.json")).toString("utf8"));
  const keys = Object.keys(obj);
  const sorted = [...keys].sort((x, y) => (x < y ? -1 : x > y ? 1 : 0));
  assert.deepEqual(keys, sorted);
});
