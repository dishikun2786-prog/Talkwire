// The IM server's side of host.openapi.yaml: every host call recorded in the
// golden transcripts (made by the reference server, which ImKit's server
// reproduces frame for frame) must be a request the contract describes, with
// a status the contract allows.

import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { Contract, contractDir, errorsOf } from "./openapi.js";

const contract = new Contract("host.openapi.yaml");
const protocolDir = path.join(contractDir, "..", "protocol");

interface HostCall {
  method: string;
  path: string;
  query?: Record<string, string>;
  body?: unknown;
  status: number;
}

function recordedCalls(): Array<{ where: string; call: HostCall }> {
  const out: Array<{ where: string; call: HostCall }> = [];
  for (const dir of ["transcripts", "transcripts-strict"]) {
    for (const file of readdirSync(path.join(protocolDir, dir))) {
      const tr = JSON.parse(readFileSync(path.join(protocolDir, dir, file), "utf8")) as { steps: Array<{ index: number; hostCalls?: HostCall[] }> };
      for (const step of tr.steps) {
        for (const call of step.hostCalls ?? []) out.push({ where: `${dir}/${file} step ${step.index}`, call });
      }
    }
  }
  return out;
}

// Normalization replaced run-specific values with placeholders; give each a
// value of the right shape (a captured variable in a topic field is a group
// topic in every scenario).
function concrete(value: unknown, key = ""): unknown {
  if (typeof value === "string") {
    return value
      .replace(/\$\{uid:[^}]+\}/g, "uid_AAAAAAAAAAA")
      .replace(/\$\{user:([^}]+)\}/g, "conf-$1")
      .replace(/\$\{[^}]+\}/g, /topic/i.test(key) ? "grp_x" : "x");
  }
  if (Array.isArray(value)) return value.map((v) => concrete(v, key));
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, concrete(v, k)]));
  return value;
}

test("every host call in the goldens is described by the contract", () => {
  const calls = recordedCalls();
  assert.ok(calls.length > 20, `only ${calls.length} host calls recorded`);
  const seen = new Set<string>();
  for (const { where, call } of calls) {
    const op = `${call.method} ${call.path}`;
    seen.add(op);
    contract.operation(call.method, call.path);
    const query = contract.query(call.method, call.path);
    assert.ok(query(concrete(call.query ?? {})), `${where} ${op}: query ${JSON.stringify(call.query)}: ${errorsOf(query)}`);
    const body = contract.requestBody(call.method, call.path);
    if (body) assert.ok(body(concrete(call.body)), `${where} ${op}: body ${JSON.stringify(call.body)}: ${errorsOf(body)}`);
    else assert.equal(call.body, undefined, `${where} ${op}: a body the contract does not describe`);
    // Throws when the status is not one the operation declares.
    contract.response(call.method, call.path, call.status);
  }
  // Periodic config refreshes are not recorded; everything else is.
  const unrecorded = contract.operations().map(([m, p]) => `${m} ${p}`).filter((op) => !seen.has(op));
  assert.deepEqual(unrecorded.sort(), ["GET /api/v1/internal/im/e2ee-config", "GET /api/v1/internal/im/runtime-config"]);
});
