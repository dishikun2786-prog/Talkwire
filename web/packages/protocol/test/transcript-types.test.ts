// Every frame in the golden transcripts must type-check against the generated
// types: `sent` frames as ClientFrame, received frames as ServerFrame. A
// transcript is what a real server put on the wire, so a frame that does not
// fit means the generator (or the schema) is wrong.
import { mkdtempSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { expect, it } from "vitest";

const protocolDir = fileURLToPath(new URL("../../../../contract/protocol/", import.meta.url));
const srcIndex = fileURLToPath(new URL("../src/index.ts", import.meta.url)).replace(/\\/g, "/");

// Sent frames that are wrong on purpose: the transcript records the server
// rejecting them (an error frame, `ok:false` for type frames, or for leave
// without topic no answer at all, section 9.4).
// The test checks each one got no success answer.
const INVALID_ON_PURPOSE = new Set<string>([
  "transcripts/core__errors.json step 5", // unknown action
  "transcripts/core__group-admin.json step 9", // memberRole "owner"
  "transcripts/core__leave.json step 16", // leave without topic
  "transcripts/core__notes.json step 14", // note without what
  "transcripts/core__push-token.json step 5", // reg_token without token
  "transcripts/module__call.json step 10", // callType "fax"
  "transcripts/module__e2ee-p2p.json step 8", // bundle without identityKey
]);

/** Placeholders the recorder wrote for values that differ between runs. */
function concrete(value: unknown): unknown {
  if (value === "<time>" || value === "<rev>") return 0;
  if (Array.isArray(value)) return value.map(concrete);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, concrete(v)]));
  }
  return value;
}

it("every golden frame fits the generated types", () => {
  const lines = [`import type { ClientFrame, ServerFrame } from "${srcIndex.replace(/\.ts$/, ".js")}";`];
  let sent = 0, received = 0;
  const rejected = new Set<string>();
  for (const dir of ["transcripts", "transcripts-strict"]) {
    for (const file of readdirSync(join(protocolDir, dir)).filter((f) => f.endsWith(".json"))) {
      const transcript = JSON.parse(readFileSync(join(protocolDir, dir, file), "utf8"));
      for (const step of transcript.steps) {
        const where = `${dir}/${file} step ${step.index}`;
        if (step.sent && INVALID_ON_PURPOSE.has(where)) {
          const answers = (step.frames?.[step.conn] ?? []) as { code?: number; ok?: boolean }[];
          const isRejection = (f: { code?: number; ok?: boolean }) => (f.code ?? 0) >= 400 || f.ok === false;
          expect(answers.every(isRejection), `${where} should be rejected`).toBe(true);
          rejected.add(where);
        } else if (step.sent) {
          lines.push(`// ${where}\nexport const c${sent++}: ClientFrame = ${JSON.stringify(concrete(step.sent))};`);
        }
        for (const frames of Object.values(step.frames ?? {}) as unknown[][]) {
          for (const frame of frames) {
            lines.push(`// ${where}\nexport const s${received++}: ServerFrame = ${JSON.stringify(concrete(frame))};`);
          }
        }
      }
    }
  }
  expect([...INVALID_ON_PURPOSE].filter((w) => !rejected.has(w)), "stale entries").toEqual([]);
  expect(sent).toBeGreaterThan(100);
  expect(received).toBeGreaterThan(200);

  const dir = mkdtempSync(join(tmpdir(), "talkwire-transcripts-"));
  const file = join(dir, "frames.ts");
  writeFileSync(file, lines.join("\n") + "\n");
  const program = ts.createProgram([file], {
    strict: true,
    noEmit: true,
    target: ts.ScriptTarget.ES2022,
    module: ts.ModuleKind.ESNext,
    moduleResolution: ts.ModuleResolutionKind.Bundler,
    skipLibCheck: true,
    lib: ["lib.es2022.d.ts", "lib.dom.d.ts"],
  });
  const problems = ts
    .getPreEmitDiagnostics(program)
    .map((d) => {
      const text = ts.flattenDiagnosticMessageText(d.messageText, "\n");
      if (!d.file || d.start === undefined) return text;
      const { line } = d.file.getLineAndCharacterOfPosition(d.start);
      const source = d.file.text.split("\n");
      return `${source[line - 1] ?? ""}\n${source[line]?.slice(0, 300)}\n  ${text.split("\n").slice(0, 4).join("\n  ")}`;
    });
  expect(problems, problems.slice(0, 8).join("\n\n")).toEqual([]);
});
