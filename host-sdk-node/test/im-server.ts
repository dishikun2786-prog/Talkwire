// Starts the real IM server for tests that need one: the binary built from
// server/ (TALKWIRE_SERVER_BIN, default ../server/bin/talkwire-server[.exe]) on the
// database TALKWIRE_TEST_DSN names. Tests skip when either is missing.

import { spawn, type ChildProcess } from "node:child_process";
import { existsSync } from "node:fs";
import { createServer } from "node:net";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");

export function serverBinary(): string | null {
  const bin = process.env.TALKWIRE_SERVER_BIN ?? path.join(repo, "server", "bin", process.platform === "win32" ? "talkwire-server.exe" : "talkwire-server");
  return existsSync(bin) ? bin : null;
}

/** Why the IM server tests cannot run here, or undefined when they can. */
export function imServerSkip(): string | undefined {
  if (!process.env.TALKWIRE_TEST_DSN) return "TALKWIRE_TEST_DSN not set";
  if (!serverBinary()) return "no server binary (go build -o bin/ in server/, or set TALKWIRE_SERVER_BIN)";
  return undefined;
}

/** key=value pairs, values optionally single-quoted with \\ escapes (libpq). */
function parseDsn(dsn: string): Record<string, string> {
  const out: Record<string, string> = {};
  const re = /(\w+)=(?:'((?:\\.|[^'\\])*)'|(\S*))/g;
  for (const m of dsn.matchAll(re)) out[m[1]!] = m[2] !== undefined ? m[2].replace(/\\(.)/g, "$1") : (m[3] ?? "");
  return out;
}

export async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.once("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const port = (srv.address() as { port: number }).port;
      srv.close(() => resolve(port));
    });
  });
}

export interface RunningServer {
  base: string;
  port: number;
  stop(): Promise<void>;
  log(): string;
}

export async function startImServer(options: { secret: string; hostUrl: string; env?: Record<string, string> }): Promise<RunningServer> {
  const db = parseDsn(process.env.TALKWIRE_TEST_DSN!);
  const port = await freePort();
  let output = "";
  const child: ChildProcess = spawn(serverBinary()!, [], {
    env: {
      ...process.env,
      DB_HOST: db.host ?? "127.0.0.1",
      DB_PORT: db.port ?? "5432",
      DB_NAME: db.dbname ?? "",
      DB_USER: db.user ?? "",
      DB_PASSWORD: db.password ?? "",
      DB_SSLMODE: db.sslmode ?? "disable",
      JWT_SECRET: options.secret,
      IM_PORT: String(port),
      MAIN_API_BASE_URL: options.hostUrl,
      ...options.env,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  child.stdout!.on("data", (d) => (output += d));
  child.stderr!.on("data", (d) => (output += d));
  const base = `http://127.0.0.1:${port}`;
  const stop = () =>
    new Promise<void>((resolve) => {
      if (child.exitCode !== null) return resolve();
      child.once("exit", () => resolve());
      child.kill();
    });
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`IM server exited during startup:\n${output}`);
    try {
      if ((await fetch(`${base}/api/health`)).ok) return { base, port, stop, log: () => output };
    } catch {
      // not listening yet
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  await stop();
  throw new Error(`IM server not healthy within 20 s:\n${output}`);
}
