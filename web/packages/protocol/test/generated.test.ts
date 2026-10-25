import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { expect, it } from "vitest";

it("generated types are up to date with the schema", () => {
  const script = fileURLToPath(new URL("../scripts/gen-types.mjs", import.meta.url));
  const out = execFileSync(process.execPath, [script, "--check"], { encoding: "utf8" });
  expect(out).toContain("up to date");
});
