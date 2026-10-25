import assert from "node:assert/strict";
import { mkdtemp, readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { after, describe, test } from "node:test";
import Fastify from "fastify";
import { LocalDiskBlobStore } from "../src/media/local.js";
import { imkitMediaRoutes, mediaDeleter, mediaName, ownerTag, type MediaOptions } from "../src/media/media.js";
import { stubIm } from "./helpers.js";

const dirs: string[] = [];
after(async () => {
  for (const d of dirs) await rm(d, { recursive: true, force: true });
});

function multipart(filename: string, data: Buffer, type = "application/octet-stream") {
  const boundary = "----imkit-test-boundary";
  const payload = Buffer.concat([
    Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="${filename}"\r\nContent-Type: ${type}\r\n\r\n`),
    data,
    Buffer.from(`\r\n--${boundary}--\r\n`),
  ]);
  return { payload, headers: { "content-type": `multipart/form-data; boundary=${boundary}` } };
}

async function setup(extra: Partial<MediaOptions> = {}, refs = 0) {
  const dir = await mkdtemp(path.join(tmpdir(), "imkit-media-"));
  dirs.push(dir);
  const store = new LocalDiskBlobStore(dir);
  const { im, seen } = stubIm((req) => (req.path.endsWith("/media-ref-count") ? { status: 200, body: { count: refs } } : { status: 500, body: { error: "x" } }));
  const app = Fastify();
  let clock = 1_800_000_000_000;
  await app.register(imkitMediaRoutes, {
    store,
    im,
    authenticate: async (req) => (req.headers["x-user"] as string | undefined) ?? null,
    maxBytes: 1024,
    now: () => clock,
    ...extra,
  });
  const upload = (user: string | null, filename: string, data: Buffer) => {
    const { payload, headers } = multipart(filename, data);
    return app.inject({ method: "POST", url: "/api/v1/chat/upload", payload, headers: user ? { ...headers, "x-user": user } : headers });
  };
  return { app, dir, store, seen, upload, advance: (ms: number) => (clock += ms) };
}

describe("media", () => {
  test("upload and serve: Content-Length, private no-store, unguessable name", async () => {
    const { app, upload } = await setup();
    const res = await upload("alice", "photo.JPG", Buffer.from("jpeg bytes"));
    assert.equal(res.statusCode, 200, res.body);
    const { url, name, mime, size } = res.json();
    assert.equal(name, "photo.JPG");
    assert.equal(mime, "image/jpeg");
    assert.equal(size, 10);
    assert.match(url, new RegExp(`^/media/m-${ownerTag("alice")}-[0-9a-z]+-[0-9a-f]{32}\\.jpg$`));
    const got = await app.inject({ method: "GET", url });
    assert.equal(got.statusCode, 200);
    assert.equal(got.body, "jpeg bytes");
    assert.equal(got.headers["content-length"], "10");
    assert.equal(got.headers["content-type"], "image/jpeg");
    assert.equal(got.headers["cache-control"], "private, no-store");
    assert.equal(got.headers["x-content-type-options"], "nosniff");
    assert.equal(got.headers["content-disposition"], "inline");
    const second = (await upload("alice", "photo.jpg", Buffer.from("x"))).json().url;
    assert.notEqual(second, url);
  });

  test("documents download as attachments", async () => {
    const { app, upload } = await setup();
    const { url } = (await upload("alice", "notes.pdf", Buffer.from("%PDF"))).json();
    assert.equal((await app.inject({ method: "GET", url })).headers["content-disposition"], "attachment");
  });

  test("refused: not signed in, wrong type, too large, empty, missing, bad names", async () => {
    const { app, upload } = await setup();
    assert.equal((await upload(null, "a.png", Buffer.from("x"))).statusCode, 401);
    assert.equal((await upload("alice", "a.svg", Buffer.from("<svg/>"))).statusCode, 400);
    assert.equal((await upload("alice", "a.exe", Buffer.from("MZ"))).statusCode, 400);
    assert.equal((await upload("alice", "a", Buffer.from("x"))).statusCode, 400);
    assert.equal((await upload("alice", "big.png", Buffer.alloc(2048))).statusCode, 413);
    assert.equal((await upload("alice", "empty.png", Buffer.alloc(0))).statusCode, 400);
    assert.equal((await app.inject({ method: "POST", url: "/api/v1/chat/upload", payload: { a: 1 }, headers: { "x-user": "alice" } })).statusCode, 400);
    for (const url of ["/media/m-00000000-1-00000000000000000000000000000000.png", "/media/..%2F..%2Fsecret", "/media/notes.txt", "/media/m-00000000-1-zz.png"]) {
      assert.equal((await app.inject({ method: "GET", url })).statusCode, 404, url);
    }
  });

  test("serveRequiresAuth", async () => {
    const { app, upload } = await setup({ serveRequiresAuth: true });
    const { url } = (await upload("alice", "a.png", Buffer.from("png"))).json();
    assert.equal((await app.inject({ method: "GET", url })).statusCode, 401);
    assert.equal((await app.inject({ method: "GET", url, headers: { "x-user": "bob" } })).statusCode, 200);
  });

  test("deleting an unsent upload: own, recent, and no message using it", async () => {
    const { app, upload, dir, seen, advance } = await setup();
    const { url } = (await upload("alice", "a.png", Buffer.from("png"))).json();
    const name = url.split("/").pop();
    const del = (user: string, n = name) => app.inject({ method: "DELETE", url: `/api/v1/chat/upload/${n}`, headers: { "x-user": user } });
    assert.equal((await del("bob")).statusCode, 404, "not bob's");
    assert.equal((await del("alice", "m-zz")).statusCode, 404);
    assert.deepEqual((await del("alice")).json(), { deleted: true });
    assert.deepEqual(await readdir(dir), []);
    assert.equal((seen[0]!.body as { url: string }).url, url);
    const late = (await upload("alice", "b.png", Buffer.from("png"))).json().url.split("/").pop();
    advance(31 * 60 * 1000);
    assert.equal((await del("alice", late)).statusCode, 404, "too old to be an unsent upload");
  });

  test("deleting is refused while a message uses the file, or when that cannot be checked", async () => {
    const used = await setup({}, 1);
    const n1 = (await used.upload("alice", "a.png", Buffer.from("png"))).json().url.split("/").pop();
    assert.equal((await used.app.inject({ method: "DELETE", url: `/api/v1/chat/upload/${n1}`, headers: { "x-user": "alice" } })).statusCode, 409);
    const down = await setup();
    const app2 = Fastify();
    const { im: failing } = stubIm(() => ({ status: 503, body: "down" }));
    await app2.register(imkitMediaRoutes, { store: down.store, im: failing, authenticate: async () => "alice" });
    const n2 = (await down.upload("alice", "a.png", Buffer.from("png"))).json().url.split("/").pop();
    assert.equal((await app2.inject({ method: "DELETE", url: `/api/v1/chat/upload/${n2}` })).statusCode, 503);
    assert.equal((await down.app.inject({ method: "GET", url: `/media/${n2}` })).statusCode, 200, "still there");
  });

  test("the host plugin's deleter removes only files these routes issued", async () => {
    const { upload, store, dir } = await setup();
    const { url } = (await upload("alice", "a.png", Buffer.from("png"))).json();
    assert.equal(mediaName(`https://chat.example${url}`), url.split("/").pop());
    assert.equal(mediaName("/images/chat-x.png"), null);
    assert.equal(mediaName("/media/../etc/passwd"), null);
    const deleter = mediaDeleter(store);
    assert.equal(await deleter([`https://chat.example${url}`, "/images/other.png", "not a url at all %%"]), 1);
    assert.deepEqual(await readdir(dir), []);
    assert.equal(await deleter([url]), 1, "a missing blob is not an error");
  });
});
