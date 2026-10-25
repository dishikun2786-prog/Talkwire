// Validate every frame in the golden transcripts against the protocol v1 schemas:
// contract/protocol/transcripts/ (legacy) and transcripts-strict/ (the strict
// profile's deviating scenarios, with its additions such as errCode).
//
// Usage: node tools/schema/check.mjs --ajv <dir containing node_modules/ajv>
//
// Frames a client sent are checked against client-frames.schema.json; frames
// the server sent (including optional ones) against server-frames.schema.json.
// Some scenarios send invalid requests on purpose; those steps are listed in
// negative-cases.json and must FAIL client validation (a passing one means the
// list or the schema is wrong).
import { createRequire } from "node:module";
import { readFileSync, readdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "..");
const args = process.argv.slice(2);
const ajvDir = args[args.indexOf("--ajv") + 1];
if (!args.includes("--ajv") || !ajvDir) {
  console.error("usage: node tools/schema/check.mjs --ajv <dir with node_modules/ajv>");
  process.exit(2);
}
const require = createRequire(join(ajvDir, "noop.js"));
const Ajv2020 = require("ajv/dist/2020").default;

const load = (p) => JSON.parse(readFileSync(p, "utf8"));
const schemaDir = join(root, "contract", "protocol", "schema");
const ajv = new Ajv2020({ allErrors: true, strict: true, strictRequired: false });
const checkClient = ajv.compile(load(join(schemaDir, "client-frames.schema.json")));
const checkServer = ajv.compile(load(join(schemaDir, "server-frames.schema.json")));
const negative = load(join(here, "negative-cases.json"));

// Placeholders back to values of the right shape.
const fakeUid = (k) => "uid_" + (k + "AAAAAAAAAAA").slice(0, 11);
const fakeTopic = (prefix, k) => prefix + (k.replace(/[^A-Za-z0-9]/g, "") + "aaaaaaaaaaaa").slice(0, prefix === "p2p_" ? 11 : 12);
function expandString(s) {
  if (s === "<time>") return 1700000000000;
  if (s === "<rev>") return 1;
  return s
    .replace(/\$\{pad:(\d+)\}/g, (_, n) => "x".repeat(Number(n)))
    .replace(/\$\{uid:([^}]+)\}/g, (_, k) => fakeUid(k))
    .replace(/\$\{user:([^}]+)\}/g, (_, k) => "conf-" + k)
    .replace(/\$\{p2p:([^}]+)\}/g, (_, k) => fakeTopic("p2p_", k))
    .replace(/\$\{([^}:]+)\}/g, (_, k) => fakeTopic("grp_", k));
}
function expand(v) {
  if (typeof v === "string") return expandString(v);
  if (Array.isArray(v)) return v.map(expand);
  if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, expand(x)]));
  return v;
}

const dirs = ["transcripts", "transcripts-strict"].map((d) => join(root, "contract", "protocol", d));
let frames = 0;
const problems = [];
for (const [dir, file] of dirs.flatMap((d) => readdirSync(d).filter((f) => f.endsWith(".json")).sort().map((f) => [d, f]))) {
  const tr = load(join(dir, file));
  const neg = new Set(negative[tr.scenario] || []);
  for (const step of tr.steps) {
    const where = `${tr.scenario} step ${step.index}`;
    if (step.sent) {
      frames++;
      const ok = checkClient(expand(step.sent));
      if (neg.has(step.index)) {
        if (ok) problems.push(`${where}: listed as a negative case but the request is valid`);
      } else if (!ok) {
        problems.push(`${where}: client frame invalid: ${ajv.errorsText(checkClient.errors)}`);
      }
    }
    for (const group of [step.frames, step.optional]) {
      for (const [conn, list] of Object.entries(group || {})) {
        for (const f of list) {
          frames++;
          if (!checkServer(expand(f))) {
            const errs = (checkServer.errors || [])
              .filter((e) => e.keyword !== "if" && e.keyword !== "oneOf")
              .map((e) => `${e.instancePath || "/"} ${e.message}${e.params?.additionalProperty ? " (" + e.params.additionalProperty + ")" : ""}`);
            problems.push(`${where} [${conn}]: server frame invalid: ${[...new Set(errs)].join("; ")}\n    ${JSON.stringify(f).slice(0, 300)}`);
          }
        }
      }
    }
  }
  for (const idx of neg) {
    if (!tr.steps.some((s) => s.index === idx && s.sent)) problems.push(`${tr.scenario}: negative case step ${idx} has no request`);
  }
}
for (const p of problems) console.log("FAIL " + p);
console.log(`SUMMARY ${frames} frame(s) checked, ${problems.length} problem(s)`);
process.exit(problems.length ? 1 : 0);
