import { ImClient } from "../src/im-client.js";
import { signInternalToken } from "../src/token.js";

export const SECRET = "test-secret-not-for-production-0123456789";
export const internalAuth = () => ({ authorization: `Bearer ${signInternalToken(SECRET)}` });

export interface SeenRequest {
  method: string;
  path: string;
  query: Record<string, string>;
  body: unknown;
  authorization: string | null;
}

export type Responder = (req: SeenRequest) => { status: number; body: unknown } | Promise<{ status: number; body: unknown }>;

/** An ImClient whose requests are recorded and answered by respond. */
export function stubIm(respond: Responder = () => ({ status: 200, body: { ok: true } })) {
  const seen: SeenRequest[] = [];
  const fetchStub = (async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(input instanceof Request ? input.url : String(input));
    const headers = new Headers(init?.headers);
    const req: SeenRequest = {
      method: init?.method ?? "GET",
      path: url.pathname,
      query: Object.fromEntries(url.searchParams),
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
      authorization: headers.get("authorization"),
    };
    seen.push(req);
    const { status, body } = await respond(req);
    return new Response(typeof body === "string" ? body : JSON.stringify(body), { status });
  }) as typeof fetch;
  const im = new ImClient({ baseUrl: "http://im.test", secret: SECRET, fetch: fetchStub });
  return { im, seen };
}

/** Lets fire-and-forget work (notifications, IM sync) run. */
export const settle = () => new Promise((r) => setTimeout(r, 20));
