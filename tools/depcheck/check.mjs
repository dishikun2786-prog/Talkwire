#!/usr/bin/env node
// Dependency direction and license checks (plan 9.2), with no dependencies of
// its own:
//
//   node tools/depcheck/check.mjs
//
// 1. Nothing in the core links libsignal or an RTC SDK: the Go server, the
//    Node host SDK, the core web packages and the core Android modules, by
//    their declared dependencies and by their imports.
// 2. Modules depend on the core, never the other way round, and never on each
//    other: a module can be installed or removed on its own.
// 3. Licenses: our packages are Apache-2.0; every runtime dependency of the
//    Node host SDK, all the way down, is under a permissive license.
//
// What it does not see: Android's resolved (transitive) Gradle graph and the
// licenses of Go and Android dependencies; CI covers those with
// `gradlew :imkit-ui:dependencies` and the module lists.

import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const root = process.argv[2] ?? join(dirname(fileURLToPath(import.meta.url)), "..", "..");

// libsignal (any binding) and the RTC SDKs the call adapters use.
const FORBIDDEN = /libsignal|signal-client|@signalapp\/|org\.signal\b|dingrtc|com\.ding\.rtc|alivc|aliyun.*rtc/i;
const WEB_CORE = ["protocol", "client", "storage-idb", "react"];
const WEB_MODULES = ["call", "group-join", "admin"];
const ANDROID_CORE = ["imkit-core", "imkit-android", "imkit-storage-room", "imkit-ui", "imkit-push-fcm"];
const PERMISSIVE = new Set(["MIT", "ISC", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "0BSD", "BlueOak-1.0.0", "Unlicense", "CC0-1.0", "Python-2.0"]);

const failures = [];
let checks = 0;
const check = (ok, message) => {
  checks++;
  if (!ok) failures.push(message);
};
const rel = (p) => relative(root, p).split("\\").join("/");
const readJson = (p) => JSON.parse(readFileSync(p, "utf8"));

function files(dir, exts) {
  if (!existsSync(dir)) return [];
  const out = [];
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name === "dist" || name === "build") continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...files(p, exts));
    else if (exts.some((e) => name.endsWith(e))) out.push(p);
  }
  return out;
}

/** Module specifiers imported by a JS/TS source. */
function imports(source) {
  const out = [];
  const re = /(?:\bfrom\s*|\bimport\s*\(\s*|\bimport\s+|\brequire\s*\(\s*)["']([^"'\n]+)["']/g;
  for (const m of source.matchAll(re)) out.push(m[1]);
  return out;
}

const imkitName = (spec) => /^@imkit\/([^/]+)/.exec(spec)?.[1] ?? null;

/** Whether an SPDX expression allows use under a permissive license. */
function permissive(expr) {
  const e = String(expr ?? "").replace(/[()]/g, " ").trim();
  if (!e) return false;
  if (/\sOR\s/.test(e)) return e.split(/\s+OR\s+/).some(permissive);
  if (/\sAND\s/.test(e)) return e.split(/\s+AND\s+/).every(permissive);
  return PERMISSIVE.has(e);
}

// ------------------------------------------------------------------ web

function webPackage(name, allowedImkit) {
  const dir = join(root, "web", "packages", name);
  const pkg = readJson(join(dir, "package.json"));
  const where = `web/packages/${name}`;
  check(pkg.license === "Apache-2.0", `${where}: license is ${pkg.license}, not Apache-2.0`);
  const declared = { ...pkg.dependencies, ...pkg.peerDependencies, ...pkg.optionalDependencies };
  const declaredImkit = new Set(Object.keys(declared).map(imkitName).filter(Boolean));
  for (const dep of Object.keys(declared)) {
    check(!FORBIDDEN.test(dep), `${where}/package.json: depends on ${dep}`);
    const other = imkitName(dep);
    if (other) check(allowedImkit.includes(other), `${where}/package.json: depends on @imkit/${other}, which it must not`);
  }
  for (const file of files(join(dir, "src"), [".ts", ".tsx"])) {
    for (const spec of imports(readFileSync(file, "utf8"))) {
      check(!FORBIDDEN.test(spec), `${rel(file)}: imports ${spec}`);
      const other = imkitName(spec);
      if (other && other !== name) {
        check(allowedImkit.includes(other), `${rel(file)}: imports @imkit/${other}, which it must not`);
        check(declaredImkit.has(other), `${rel(file)}: imports @imkit/${other} without declaring it`);
      }
    }
  }
}

if (existsSync(join(root, "web", "packages"))) {
  // The core in layers: each may use only the layers below it.
  webPackage("protocol", []);
  webPackage("client", ["protocol"]);
  webPackage("storage-idb", ["protocol", "client"]);
  webPackage("react", ["protocol", "client"]);
  // Modules: the core only, never another module.
  for (const m of WEB_MODULES) if (existsSync(join(root, "web", "packages", m))) webPackage(m, WEB_CORE);
  const present = readdirSync(join(root, "web", "packages")).filter((d) => existsSync(join(root, "web", "packages", d, "package.json")));
  for (const d of present) check([...WEB_CORE, ...WEB_MODULES].includes(d), `web/packages/${d}: not classified as core or module in tools/depcheck`);
}

// ------------------------------------------------------------------ Node host SDK

/** Resolves a package the way Node does: node_modules of dir and its parents. */
function resolvePackage(fromDir, name) {
  for (let d = fromDir; ; d = dirname(d)) {
    const p = join(d, "node_modules", name, "package.json");
    if (existsSync(p)) return p;
    if (dirname(d) === d) return null;
  }
}

function runtimeLicenses(pkgDir, where) {
  const seen = new Set();
  const visit = (dir) => {
    const pkg = readJson(join(dir, "package.json"));
    for (const dep of Object.keys({ ...pkg.dependencies, ...pkg.optionalDependencies })) {
      const p = resolvePackage(dir, dep);
      if (!p) {
        check(!pkg.dependencies?.[dep], `${where}: ${dep} (needed by ${pkg.name}) is not installed; run npm install`);
        continue;
      }
      if (seen.has(p)) continue;
      seen.add(p);
      const dpkg = readJson(p);
      check(!FORBIDDEN.test(dep), `${where}: runtime dependency ${dep}`);
      const license = dpkg.license ?? (Array.isArray(dpkg.licenses) ? dpkg.licenses.map((l) => l.type ?? l).join(" OR ") : undefined);
      check(permissive(license), `${where}: runtime dependency ${dpkg.name}@${dpkg.version} is under ${license ?? "no stated license"}`);
      visit(dirname(p));
    }
  };
  visit(pkgDir);
  return seen.size;
}

const hostDir = join(root, "host-sdk-node");
if (existsSync(hostDir)) {
  const pkg = readJson(join(hostDir, "package.json"));
  check(pkg.license === "Apache-2.0", `host-sdk-node: license is ${pkg.license}, not Apache-2.0`);
  for (const file of files(join(hostDir, "src"), [".ts"])) {
    for (const spec of imports(readFileSync(file, "utf8"))) check(!FORBIDDEN.test(spec), `${rel(file)}: imports ${spec}`);
  }
  if (existsSync(join(hostDir, "node_modules"))) runtimeLicenses(hostDir, "host-sdk-node");
  else failures.push("host-sdk-node: node_modules missing; run npm install there first");
}

// ------------------------------------------------------------------ Go server

for (const f of ["go.mod", "go.sum"]) {
  const p = join(root, "server", f);
  if (!existsSync(p)) continue;
  for (const line of readFileSync(p, "utf8").split("\n")) check(!FORBIDDEN.test(line), `server/${f}: ${line.trim()}`);
}
for (const file of files(join(root, "server"), [".go"])) {
  const src = readFileSync(file, "utf8");
  const block = /import\s*(\([\s\S]*?\)|"[^"]+")/g;
  for (const m of src.matchAll(block)) check(!FORBIDDEN.test(m[1]), `${rel(file)}: imports ${m[1].replace(/\s+/g, " ")}`);
}

// ------------------------------------------------------------------ Android

for (const mod of ANDROID_CORE) {
  const dir = join(root, "android", mod);
  if (!existsSync(dir)) continue;
  const gradle = join(dir, "build.gradle.kts");
  if (existsSync(gradle)) {
    for (const line of readFileSync(gradle, "utf8").split("\n")) {
      if (line.trim().startsWith("//")) continue;
      check(!FORBIDDEN.test(line), `android/${mod}/build.gradle.kts: ${line.trim()}`);
      const project = /project\(\s*":([^"]+)"\s*\)/.exec(line)?.[1];
      if (project) check(ANDROID_CORE.includes(project), `android/${mod}: depends on :${project}, which is not core`);
    }
  }
  for (const file of files(join(dir, "src"), [".kt", ".java"])) {
    for (const m of readFileSync(file, "utf8").matchAll(/^\s*import\s+([\w.]+)/gm)) check(!FORBIDDEN.test(m[1]), `${rel(file)}: imports ${m[1]}`);
  }
}

// ------------------------------------------------------------------ report

if (failures.length) {
  console.error(`depcheck: ${failures.length} of ${checks} checks failed`);
  for (const f of failures) console.error("  " + f);
  process.exit(1);
}
console.log(`depcheck: OK (${checks} checks)`);
