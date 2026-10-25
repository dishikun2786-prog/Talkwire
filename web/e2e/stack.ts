/**
 * The stack the end-to-end tests run against: the sample host, a real
 * `imkit-server` in host mode on a throwaway PostgreSQL database, and the web
 * sample's Vite dev server in front of both. It uses its own ports, so a dev
 * stack already running on 5173/6001/6062 is left alone.
 *
 * Needs IMKIT_TEST_DSN (libpq key=value, the variable the server's database
 * tests use) and a built `server/bin/imkit-server(.exe)`. The DSN's password
 * goes to the server through its environment and is never logged.
 */

import { spawn, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import { createWriteStream, existsSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const web = fileURLToPath(new URL("../", import.meta.url));
const root = join(web, "..");
const serverDir = join(root, "server");
const binary = join(serverDir, process.platform === "win32" ? "bin/imkit-server.exe" : "bin/imkit-server");
const hostDir = join(root, "examples", "sample-host");
export const logDir = join(web, "e2e", ".stack-logs");

export const ports = {
  sample: Number(process.env.E2E_SAMPLE_PORT ?? 15173),
  host: Number(process.env.E2E_HOST_PORT ?? 16001),
  im: Number(process.env.E2E_IM_PORT ?? 16062),
};
export const baseURL = `http://127.0.0.1:${ports.sample}`;

/** libpq key=value pairs; values may be single-quoted with backslash escapes. */
function parseDsn(dsn: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const m of dsn.matchAll(/(\w+)\s*=\s*('((?:\\.|[^'\\])*)'|\S+)/g)) {
    out[m[1]!] = m[2]!.startsWith("'") ? m[3]!.replace(/\\(.)/g, "$1") : m[2]!;
  }
  return out;
}

/** The environment without the DSN, so no child (or its logs) sees it whole. */
function baseEnv(): NodeJS.ProcessEnv {
  const env = { ...process.env };
  delete env.IMKIT_TEST_DSN;
  return env;
}

interface Child {
  name: string;
  proc: ChildProcess;
  log: string;
}

function start(name: string, command: string, args: string[], cwd: string, env: NodeJS.ProcessEnv): Child {
  const log = join(logDir, name + ".log");
  const out = createWriteStream(log);
  const proc = spawn(command, args, { cwd, env, stdio: ["ignore", "pipe", "pipe"], windowsHide: true });
  proc.stdout!.pipe(out);
  proc.stderr!.pipe(out);
  return { name, proc, log };
}

function tail(c: Child, lines = 40): string {
  try {
    return readFileSync(c.log, "utf8").split(/\r?\n/).slice(-lines).join("\n");
  } catch {
    return "(no log)";
  }
}

/** Waits until `url` answers at all (any status), or throws with the child's log. */
async function waitForHttp(url: string, child: Child, timeoutMs = 90_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (child.proc.exitCode !== null) throw new Error(`${child.name} exited (${child.proc.exitCode}):\n${tail(child)}`);
    try {
      await fetch(url, { signal: AbortSignal.timeout(2_000) });
      return;
    } catch {
      await new Promise((r) => setTimeout(r, 300));
    }
  }
  throw new Error(`${child.name} did not answer at ${url} in time:\n${tail(child)}`);
}

async function portFree(port: number): Promise<boolean> {
  try {
    await fetch(`http://127.0.0.1:${port}/`, { signal: AbortSignal.timeout(1_000) });
    return false;
  } catch (e) {
    return (e as { cause?: { code?: string } }).cause?.code === "ECONNREFUSED";
  }
}

export interface Stack {
  stop(): Promise<void>;
}

export async function startStack(): Promise<Stack> {
  const dsn = process.env.IMKIT_TEST_DSN;
  if (!dsn) throw new Error("IMKIT_TEST_DSN is not set: the end-to-end tests need a throwaway PostgreSQL database (see web/README.md).");
  if (!existsSync(binary)) throw new Error(`${binary} is missing: build it with \`cd server && go build -o bin/ .\`.`);
  for (const [what, port] of Object.entries(ports)) {
    if (!(await portFree(port))) throw new Error(`port ${port} (${what}) is in use; set E2E_${what.toUpperCase()}_PORT to another one`);
  }
  rmSync(logDir, { recursive: true, force: true });
  mkdirSync(logDir, { recursive: true });
  const media = join(logDir, "media");
  mkdirSync(media, { recursive: true });

  const db = parseDsn(dsn);
  const secret = randomBytes(32).toString("hex");
  const children: Child[] = [];
  const stop = async () => {
    for (const c of children.reverse()) {
      if (c.proc.exitCode === null) c.proc.kill();
    }
    await Promise.all(children.map((c) => (c.proc.exitCode === null ? new Promise((r) => c.proc.once("exit", r)) : null)));
  };

  try {
    const host = start("sample-host", process.execPath, ["--import", "tsx", "src/main.ts"], hostDir, {
      ...baseEnv(),
      JWT_SECRET: secret,
      PORT: String(ports.host),
      IM_BASE_URL: `http://127.0.0.1:${ports.im}`,
      MEDIA_DIR: media,
    });
    children.push(host);
    const im = start("imkit-server", binary, [], serverDir, {
      ...baseEnv(),
      IM_PORT: String(ports.im),
      DB_HOST: db.host ?? "127.0.0.1",
      DB_PORT: db.port ?? "5432",
      DB_USER: db.user ?? "postgres",
      DB_PASSWORD: db.password ?? "",
      DB_NAME: db.dbname ?? "",
      DB_SSLMODE: db.sslmode ?? "disable",
      JWT_SECRET: secret,
      MAIN_API_BASE_URL: `http://127.0.0.1:${ports.host}`,
      IMKIT_HOST_MODE: "http",
      IMKIT_PROTOCOL_PROFILE: "strict",
    });
    children.push(im);
    const vite = start("vite", process.execPath, [join(web, "node_modules", "vite", "bin", "vite.js"), "sample", "--config", "sample/vite.config.ts"], web, {
      ...baseEnv(),
      SAMPLE_PORT: String(ports.sample),
      SAMPLE_HOST_URL: `http://127.0.0.1:${ports.host}`,
      SAMPLE_IM_URL: `http://127.0.0.1:${ports.im}`,
    });
    children.push(vite);

    await waitForHttp(`http://127.0.0.1:${ports.im}/api/health`, im);
    await waitForHttp(`http://127.0.0.1:${ports.host}/`, host);
    await waitForHttp(baseURL + "/", vite);
  } catch (e) {
    await stop();
    throw e;
  }
  return { stop };
}
