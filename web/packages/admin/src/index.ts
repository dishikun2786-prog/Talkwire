/**
 * @talkwire/admin: administrator pages for Talkwire, embedded in the host's own
 * admin console. Chat records (a user's conversations and messages, and
 * clearing a two-person conversation for both users) and call records, over
 * the host's talkwireAdminRoutes (@talkwire/host-node).
 *
 *   const api = new ImAdminApi({ headers: () => ({ authorization: `Bearer ${token}` }) });
 *   <ImAdminProvider api={api} role="superadmin" locale="zh">
 *     <AdminStyles />
 *     <ChatRecords />
 *   </ImAdminProvider>
 */

export * from "./api.js";
export * from "./strings.js";
export * from "./components.js";
export * from "./pages.js";
