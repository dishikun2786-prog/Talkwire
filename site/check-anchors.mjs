// VitePress fails the build on links to pages that do not exist, but not on
// links to a heading that does not exist (/server/env#push when the page has
// no #push). This walks the built site and checks every in-site link with a
// fragment against the ids on the target page.
//
//   node check-anchors.mjs [dist dir]      (default .vitepress/dist)

import { readdirSync, readFileSync, existsSync } from "node:fs";
import { join, relative, sep } from "node:path";

const dist = process.argv[2] ?? join(".vitepress", "dist");
if (!existsSync(dist)) {
  console.error(`no build at ${dist}: run the site build first`);
  process.exit(2);
}

function* htmlFiles(dir) {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) yield* htmlFiles(p);
    else if (e.name.endsWith(".html")) yield p;
  }
}

// "/guide/" → guide/index.html, "/server/env" → server/env.html
function pageFile(path) {
  const clean = decodeURIComponent(path).replace(/^\//, "");
  if (clean === "" || clean.endsWith("/")) return join(dist, clean, "index.html");
  return join(dist, clean.replace(/\.html$/, "") + ".html");
}

const ids = new Map();
function idsOf(file) {
  if (!ids.has(file)) {
    const html = readFileSync(file, "utf8");
    ids.set(file, new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => decodeEntities(m[1]))));
  }
  return ids.get(file);
}
const decodeEntities = (s) => s.replace(/&amp;/g, "&").replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, "<").replace(/&gt;/g, ">");

let bad = 0;
let checked = 0;
for (const file of htmlFiles(dist)) {
  const html = readFileSync(file, "utf8");
  const self = "/" + relative(dist, file).split(sep).join("/").replace(/index\.html$/, "").replace(/\.html$/, "");
  for (const m of html.matchAll(/<a\s[^>]*href="([^"]*#[^"]+)"/g)) {
    const href = decodeEntities(m[1]);
    if (/^[a-z]+:/i.test(href)) continue; // other sites
    const [path, frag] = href.split("#");
    if (frag === "" || frag === undefined) continue;
    const target = path === "" ? file : pageFile(path.startsWith("/") ? path : new URL(path, "http://x" + self).pathname);
    checked++;
    if (!existsSync(target)) continue; // missing pages are VitePress's own check
    if (!idsOf(target).has(decodeURIComponent(frag))) {
      bad++;
      console.log(`${self}: ${href} (no #${decodeURIComponent(frag)} on that page)`);
    }
  }
}
console.log(`checked ${checked} links with a fragment, ${bad} broken`);
process.exit(bad ? 1 : 0);
