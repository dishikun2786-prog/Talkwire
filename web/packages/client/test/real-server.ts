/**
 * A real `imkit-server` in standalone mode (no host process) on a throwaway
 * PostgreSQL database. Needs `server/bin/imkit-server(.exe)` and
 * IMKIT_TEST_DSN (libpq key=value, the variable the server's own database
 * tests use); without either the tests skip. The DSN's password goes to the
 * server through its environment and is never logged.
 */

import { spawn, type ChildProcess } from "node:child_process";
import { createHmac, randomBytes } from "node:crypto";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { repoRoot } from "./replay.js";

const serverDir = join(repoRoot, "server");
const binary = join(serverDir, process.platform === "win32" ? "bin/imkit-server.exe" : "bin/imkit-server");
export const realServerAvailable = existsSync(binary) && !!process.env.IMKIT_TEST_DSN;

/** libpq key=value pairs; values may be single-quoted with backslash escapes. */
export function parseDsn(dsn: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const m of dsn.matchAll(/(\w+)\s*=\s*('((?:\\.|[^'\\])*)'|\S+)/g)) {
    out[m[1]!] = m[2]!.startsWith("'") ? m[3]!.replace(/\\(.)/g, "$1") : m[2]!;
  }
  return out;
}

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const s = createServer();
    s.listen(0, "127.0.0.1", () => {
      const port = (s.address() as { port: number }).port;
      s.close(() => resolve(port));
    });
    s.on("error", reject);
  });
}

const b64 = (b: Buffer | string) => Buffer.from(b).toString("base64url");

export class RealServer {
  private constructor(
    private readonly proc: ChildProcess,
    readonly port: number,
    private readonly secret: string,
    private readonly log: string[],
    private readonly tmp: string,
  ) {}

  get wsUrl(): string {
    return `ws://127.0.0.1:${this.port}/ws/im`;
  }

  get httpUrl(): string {
    return `http://127.0.0.1:${this.port}`;
  }

  logTail(lines = 60): string {
    return this.log.join("").split("\n").slice(-lines).join("\n");
  }

  /** An HS256 access token for app user `sub`, as the host would issue it. */
  token(sub: string, type = "access", role = "app_user"): string {
    const now = Math.floor(Date.now() / 1000);
    const head = b64(JSON.stringify({ alg: "HS256", typ: "JWT" }));
    const body = b64(JSON.stringify({ sub, type, role, iat: now, exp: now + 3600 }));
    return `${head}.${body}.${b64(createHmac("sha256", this.secret).update(`${head}.${body}`).digest())}`;
  }

  async close(): Promise<void> {
    if (this.proc.exitCode === null) {
      this.proc.kill();
      await new Promise((r) => setTimeout(r, 300));
    }
    rmSync(this.tmp, { recursive: true, force: true });
  }

  /** Starts the server with protocol `profile` and waits for its health check. */
  static async start(profile: "strict" | "legacy"): Promise<RealServer> {
    const db = parseDsn(process.env.IMKIT_TEST_DSN!);
    if (!db.dbname) throw new Error("IMKIT_TEST_DSN has no dbname");
    const port = await freePort();
    const secret = randomBytes(32).toString("hex");
    const tmp = mkdtempSync(join(tmpdir(), "imkit-standalone-"));
    const config = join(tmp, "standalone.json");
    writeFileSync(config, '{"relations":"allow-all"}');
    const proc = spawn(binary, [], {
      cwd: serverDir,
      stdio: ["ignore", "pipe", "pipe"],
      env: {
        ...process.env,
        IM_PORT: String(port),
        DB_HOST: db.host ?? "127.0.0.1",
        DB_PORT: db.port ?? "5432",
        DB_USER: db.user ?? "postgres",
        DB_PASSWORD: db.password ?? "",
        DB_NAME: db.dbname,
        DB_SSLMODE: db.sslmode ?? "disable",
        JWT_SECRET: secret,
        IMKIT_HOST_MODE: "standalone",
        IMKIT_STANDALONE_CONFIG: config,
        IMKIT_PROTOCOL_PROFILE: profile,
        IM_CALL_DISCONNECT_GRACE_SEC: "1",
      },
    });
    const log: string[] = [];
    proc.stdout!.on("data", (c: Buffer) => log.push(c.toString()));
    proc.stderr!.on("data", (c: Buffer) => log.push(c.toString()));
    const server = new RealServer(proc, port, secret, log, tmp);
    const deadline = Date.now() + 30_000;
    for (;;) {
      if (proc.exitCode !== null) {
        await server.close();
        throw new Error("imkit-server exited:\n" + server.logTail());
      }
      const healthy = await fetch(`${server.httpUrl}/api/health`).then((r) => r.status === 200, () => false);
      if (healthy) return server;
      if (Date.now() > deadline) {
        await server.close();
        throw new Error("imkit-server not healthy in 30s:\n" + server.logTail());
      }
      await new Promise((r) => setTimeout(r, 100));
    }
  }
}
