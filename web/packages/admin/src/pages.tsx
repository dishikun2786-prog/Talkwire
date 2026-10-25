/**
 * The pages as a route list, for whatever router the host's console uses.
 * Paths are relative to where the host mounts them; `go` takes such a path.
 *
 *   // react-router
 *   {imAdminPages.map((p) => <Route key={p.path} path={p.path} element={<Page page={p} />} />)}
 *   function Page({ page }) {
 *     const params = useParams(); const navigate = useNavigate();
 *     return page.render({ params, go: (to) => navigate(`/admin/${to}`) });
 *   }
 *
 * A console without a router can use matchImAdminPage on its own path.
 */

import type { ReactNode } from "react";
import { CallDetail, CallRecords, ChatRecords } from "./components.js";
import type { AdminStrings } from "./strings.js";

export interface ImAdminPageNav {
  params: Readonly<Record<string, string | undefined>>;
  go(path: string): void;
}

export interface ImAdminPage {
  /** e.g. "im/calls/:callSessionId". */
  path: string;
  /** The menu entry; null for a page reached from another (a call's details). */
  title: ((strings: AdminStrings) => string) | null;
  render(nav: ImAdminPageNav): ReactNode;
}

export const imAdminPages: readonly ImAdminPage[] = [
  { path: "im/chats", title: (s) => s.chatRecords, render: () => <ChatRecords /> },
  {
    path: "im/calls",
    title: (s) => s.callRecords,
    render: ({ go }) => <CallRecords onOpen={(id) => go(`im/calls/${encodeURIComponent(id)}`)} />,
  },
  {
    path: "im/calls/:callSessionId",
    title: null,
    render: ({ params, go }) => <CallDetail callSessionId={params["callSessionId"] ?? ""} onBack={() => go("im/calls")} />,
  },
];

/** The page a path names, with its decoded parameters; null when none does. */
export function matchImAdminPage(path: string, pages: readonly ImAdminPage[] = imAdminPages): { page: ImAdminPage; params: Record<string, string> } | null {
  const parts = path.replace(/^\/+|\/+$/g, "").split("/");
  for (const page of pages) {
    const pattern = page.path.split("/");
    if (pattern.length !== parts.length) continue;
    const params: Record<string, string> = {};
    const fits = pattern.every((seg, i) => {
      const part = parts[i]!;
      if (!seg.startsWith(":")) return seg === part;
      try {
        params[seg.slice(1)] = decodeURIComponent(part);
        return part.length > 0;
      } catch {
        return false;
      }
    });
    if (fits) return { page, params };
  }
  return null;
}
