/**
 * One run of `tools/conformance/bin/replay`: it plays the server's side of a
 * golden transcript to the client under test and exits 0 only if the client
 * sent exactly the scripted requests. Tests skip when the binary has not been
 * built (`cd tools/conformance && go build -o bin/replay.exe ./cmd/replay`).
 *
 * Scenario user X logs in as app user "conf-X" (tools/conformance/harness).
 */

import { spawn, execFileSync, type ChildProcess } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { WebSocketTransport, type ImSocket, type ImSocketListener, type ImTransport } from "../src/index.js";

export const repoRoot = fileURLToPath(new URL("../../../../", import.meta.url));
const conformance = join(repoRoot, "tools/conformance");
const binary = join(conformance, process.platform === "win32" ? "bin/replay.exe" : "bin/replay");
export const replayBuilt = existsSync(binary);

const baseArgs = (scenario: string) => [
  "-scenarios", join(repoRoot, "contract/protocol/scenarios"),
  "-transcripts", join(repoRoot, "contract/protocol/transcripts"),
  "-scenario", scenario,
];

/** The concrete value of every placeholder of `scenario`, keyed like "uid:A". */
export function bindings(scenario: string): Record<string, string> {
  const text = execFileSync(binary, [...baseArgs(scenario), "-bindings"], { cwd: conformance, encoding: "utf8" });
  const out: Record<string, string> = {};
  for (const line of text.split(/\r?\n/)) {
    const m = /^\$\{(.+)\} = (.*)$/.exec(line.trim());
    if (m) out[m[1]!] = m[2]!;
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

export class Replay {
  private exitCode: number | null = null;
  private readonly exited: Promise<number>;

  private constructor(
    private readonly proc: ChildProcess,
    readonly port: number,
    private readonly log: string[],
    private readonly tmp: string | null,
  ) {
    this.exited = new Promise((resolve) => proc.on("exit", (code) => resolve((this.exitCode = code ?? -1))));
  }

  get url(): string {
    return `ws://127.0.0.1:${this.port}/`;
  }

  get output(): string {
    return this.log.join("");
  }

  /** Starts replaying `scenario` as scenario user `user`; resolves once it listens. */
  static async start(scenario: string, user: string, faults?: string, ...extra: string[]): Promise<Replay> {
    const port = await freePort();
    let tmp: string | null = null;
    const args = [...baseArgs(scenario), "-user", user, "-addr", `127.0.0.1:${port}`];
    if (faults) {
      tmp = mkdtempSync(join(tmpdir(), "replay-faults-"));
      const file = join(tmp, "faults.json");
      writeFileSync(file, faults);
      args.push("-faults", "@" + file);
    }
    args.push(...extra);
    const proc = spawn(binary, args, { cwd: conformance, stdio: ["ignore", "pipe", "pipe"] });
    const log: string[] = [];
    const replay = new Replay(proc, port, log, tmp);
    await new Promise<void>((resolve, reject) => {
      const t = setTimeout(() => reject(new Error("replay did not start:\n" + log.join(""))), 10_000);
      const onData = (chunk: Buffer) => {
        log.push(chunk.toString());
        if (log.join("").includes("replaying ")) {
          clearTimeout(t);
          resolve();
        }
      };
      proc.stdout!.on("data", onData);
      proc.stderr!.on("data", (c: Buffer) => log.push(c.toString()));
      proc.on("exit", () => {
        clearTimeout(t);
        if (!log.join("").includes("replaying ")) reject(new Error("replay exited:\n" + log.join("")));
      });
    });
    return replay;
  }

  /** Waits for the replay to finish and throws unless the client followed the script. */
  async assertPassed(timeoutMs = 30_000): Promise<void> {
    const code = await Promise.race([this.exited, new Promise<null>((r) => setTimeout(() => r(null), timeoutMs))]);
    if (code === null) {
      this.proc.kill();
      throw new Error("replay did not finish in time:\n" + this.output);
    }
    if (code !== 0 || !this.output.includes("PASS")) throw new Error("replay failed:\n" + this.output);
  }

  close(): void {
    if (this.exitCode === null) this.proc.kill();
    if (this.tmp) rmSync(this.tmp, { recursive: true, force: true });
  }
}

/** Runs `block` against a replay, then requires the replay to pass; failures carry its output. */
export async function replaying(scenario: string, user: string, faults: string | undefined, block: (r: Replay) => Promise<void>, ...extra: string[]): Promise<void> {
  const replay = await Replay.start(scenario, user, faults, ...extra);
  try {
    try {
      await block(replay);
    } catch (e) {
      throw new Error(`${e instanceof Error ? e.stack ?? e.message : String(e)}\n--- replay output so far ---\n${replay.output}`);
    }
    await replay.assertPassed();
  } finally {
    replay.close();
  }
}

/** Passes everything through and keeps a copy of every frame the client sends. */
export class RecordingTransport implements ImTransport {
  readonly sent: Record<string, unknown>[] = [];

  constructor(private readonly delegate: ImTransport = new WebSocketTransport()) {}

  sentActions(action: string): Record<string, unknown>[] {
    return this.sent.filter((f) => f.action === action);
  }

  open(url: string, listener: ImSocketListener): ImSocket {
    const socket = this.delegate.open(url, listener);
    return {
      send: (text) => {
        try {
          this.sent.push(JSON.parse(text));
        } catch {
          // not JSON: not recorded
        }
        return socket.send(text);
      },
      close: (code, reason) => socket.close(code, reason),
    };
  }
}

export { until } from "./until.js";
