/**
 * The sample's admin console at `#/admin/...`: the @talkwire/admin pages behind
 * the sample host's /dev/admin-login. A real console signs its
 * administrators in its own way and passes their credentials as headers.
 */

import { AdminStyles, ImAdminApi, ImAdminProvider, adminStringsFor, imAdminPages, matchImAdminPage, type AdminRole } from "@talkwire/admin";
import { useEffect, useMemo, useState } from "react";

interface AdminSession {
  adminId: string;
  role: AdminRole;
  token: string;
}

const KEY = "talkwire-sample-admin";

function savedAdmin(): AdminSession | null {
  try {
    const s = JSON.parse(sessionStorage.getItem(KEY) ?? "null") as AdminSession | null;
    return s && typeof s.token === "string" ? s : null;
  } catch {
    return null;
  }
}

/** The page path after `#/admin/`. */
export const adminPath = () => (/^#\/admin\/?(.*)$/.exec(location.hash)?.[1] ?? "").replace(/\/$/, "");
export const isAdminHash = () => /^#\/admin(\/|$)/.test(location.hash);

const text = {
  en: { console: "Admin console", name: "Administrator", role: "Role", signIn: "Sign in", signOut: "Sign out", chat: "Back to chat" },
  zh: { console: "管理后台", name: "管理员", role: "角色", signIn: "登录", signOut: "退出", chat: "返回聊天" },
  vi: { console: "Trang quản trị", name: "Quản trị viên", role: "Vai trò", signIn: "Đăng nhập", signOut: "Đăng xuất", chat: "Quay lại trò chuyện" },
};

export function AdminConsole({ locale }: { locale: "en" | "zh" | "vi" }) {
  const t = text[locale];
  const [session, setSession] = useState<AdminSession | null>(savedAdmin);
  const [path, setPath] = useState(adminPath);
  const [error, setError] = useState<string | null>(null);
  const api = useMemo(() => (session ? new ImAdminApi({ headers: () => ({ authorization: `Bearer ${session.token}` }) }) : null), [session]);

  useEffect(() => {
    const onHash = () => setPath(adminPath());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  const signIn = async (name: string, role: AdminRole) => {
    setError(null);
    const res = await fetch("/dev/admin-login", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ name, role }) });
    const body = (await res.json()) as AdminSession & { error?: string };
    if (!res.ok) return setError(body.error ?? `HTTP ${res.status}`);
    sessionStorage.setItem(KEY, JSON.stringify(body));
    setSession(body);
  };

  const signOut = () => {
    sessionStorage.removeItem(KEY);
    setSession(null);
  };

  if (!session || !api) {
    return (
      <form
        className="sample-login"
        aria-label={t.console}
        onSubmit={(e) => {
          e.preventDefault();
          const data = new FormData(e.currentTarget);
          void signIn(String(data.get("name") ?? "").trim(), data.get("role") === "superadmin" ? "superadmin" : "admin");
        }}
      >
        <strong>{t.console}</strong>
        <label>
          {t.name}
          <input name="name" required pattern="[A-Za-z0-9_]{2,20}" autoComplete="username" />
        </label>
        <label>
          {t.role}
          <select name="role" defaultValue="admin">
            <option value="admin">admin</option>
            <option value="superadmin">superadmin</option>
          </select>
        </label>
        <button type="submit">{t.signIn}</button>
        {error ? (
          <p className="sample-error" role="alert">
            {error}
          </p>
        ) : null}
      </form>
    );
  }

  const strings = adminStringsFor(locale);
  const found = matchImAdminPage(path) ?? matchImAdminPage(imAdminPages[0]!.path)!;
  return (
    <ImAdminProvider api={api} role={session.role} locale={locale}>
      <AdminStyles />
      <nav className="sample-admin-nav" aria-label={t.console}>
        {imAdminPages.map((p) =>
          p.title ? (
            <a key={p.path} href={`#/admin/${p.path}`} aria-current={found.page === p ? "page" : undefined}>
              {p.title(strings)}
            </a>
          ) : null,
        )}
        <span style={{ flex: 1 }} />
        <span className="sample-me" data-testid="admin">
          {session.adminId} · {session.role}
        </span>
        <button type="button" onClick={signOut}>
          {t.signOut}
        </button>
        <a href="#/">{t.chat}</a>
      </nav>
      <main className="sample-admin-main" key={path}>
        {found.page.render({
          params: found.params,
          go: (to) => {
            location.hash = `#/admin/${to}`;
          },
        })}
      </main>
    </ImAdminProvider>
  );
}
