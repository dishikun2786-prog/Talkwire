// Needs DATABASE_URL pointing at a throwaway PostgreSQL database with the
// tables of prisma/imkit.prisma (npx prisma db push --schema prisma).

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { after, test } from "node:test";
import { fileURLToPath } from "node:url";
import { PrismaClient } from "@prisma/client";
import { prismaStores } from "../src/prisma/index.js";
import { storeSuite } from "./stores.suite.js";

const skip = process.env.DATABASE_URL ? undefined : "DATABASE_URL not set";
const prisma = skip ? undefined : new PrismaClient();

after(async () => {
  await prisma?.$disconnect();
});

storeSuite(
  "prisma",
  async () => {
    await prisma!.$executeRawUnsafe("TRUNCATE imkit_profiles, imkit_contacts, imkit_admin_audit, imkit_call_records");
    const stores = prismaStores(prisma!);
    return { ...stores, calls: stores.calls! };
  },
  { skip },
);

test("prisma/imkit.sql is the DDL of prisma/imkit.prisma", () => {
  const cli = fileURLToPath(new URL("../node_modules/prisma/build/index.js", import.meta.url));
  const generated = execFileSync(process.execPath, [cli, "migrate", "diff", "--from-empty", "--to-schema-datamodel", "prisma", "--script"], {
    cwd: fileURLToPath(new URL("..", import.meta.url)),
    encoding: "utf8",
    env: { ...process.env, DATABASE_URL: process.env.DATABASE_URL ?? "postgresql://unused@localhost:1/unused" },
  });
  assert.equal(generated.replace(/\r\n/g, "\n"), readFileSync(new URL("../prisma/imkit.sql", import.meta.url), "utf8"), "run npm run prisma:sql > prisma/imkit.sql");
});
