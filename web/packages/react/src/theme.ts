/**
 * The look of the components, all in CSS variables on `.imkit-root` (5.9).
 * An app themes the kit by setting the variables on any ancestor, e.g.
 *
 *     .my-app { --imkit-accent: #e91e63; --imkit-radius: 6px; }
 *
 * Dark colours apply with `prefers-color-scheme: dark` unless the root has
 * `data-theme="light"`; `data-theme="dark"` on the root forces them.
 */

export const imkitCss = `
.imkit-root {
  --imkit-font: system-ui, -apple-system, "Segoe UI", Roboto, "Noto Sans SC", sans-serif;
  --imkit-bg: #f3f4f6;
  --imkit-surface: #ffffff;
  --imkit-text: #111827;
  --imkit-text-muted: #6b7280;
  --imkit-border: #e5e7eb;
  --imkit-accent: #2563eb;
  --imkit-on-accent: #ffffff;
  --imkit-bubble-mine: #2563eb;
  --imkit-bubble-mine-text: #ffffff;
  --imkit-bubble-other: #ffffff;
  --imkit-bubble-other-text: #111827;
  --imkit-danger: #dc2626;
  --imkit-online: #16a34a;
  --imkit-radius: 14px;
  font-family: var(--imkit-font);
  color: var(--imkit-text);
  background: var(--imkit-bg);
  box-sizing: border-box;
}
.imkit-root *, .imkit-root *::before, .imkit-root *::after { box-sizing: inherit; }
@media (prefers-color-scheme: dark) {
  .imkit-root:not([data-theme="light"]) {
    --imkit-bg: #0b0f17; --imkit-surface: #151b26; --imkit-text: #e5e7eb; --imkit-text-muted: #9ca3af;
    --imkit-border: #273041; --imkit-bubble-other: #1f2937; --imkit-bubble-other-text: #e5e7eb;
    --imkit-accent: #3b82f6; --imkit-bubble-mine: #2563eb;
  }
}
.imkit-root[data-theme="dark"] {
  --imkit-bg: #0b0f17; --imkit-surface: #151b26; --imkit-text: #e5e7eb; --imkit-text-muted: #9ca3af;
  --imkit-border: #273041; --imkit-bubble-other: #1f2937; --imkit-bubble-other-text: #e5e7eb;
  --imkit-accent: #3b82f6; --imkit-bubble-mine: #2563eb;
}
.imkit-avatar { flex: none; border-radius: 50%; background: var(--imkit-accent); color: var(--imkit-on-accent);
  display: inline-flex; align-items: center; justify-content: center; font-weight: 600; overflow: hidden; position: relative; }
.imkit-avatar img { width: 100%; height: 100%; object-fit: cover; }
.imkit-dot { position: absolute; right: 0; bottom: 0; width: 28%; height: 28%; border-radius: 50%;
  background: var(--imkit-online); border: 2px solid var(--imkit-surface); }
.imkit-banner { padding: 6px 12px; font-size: 13px; text-align: center; background: #fef3c7; color: #92400e; }
.imkit-list { display: flex; flex-direction: column; background: var(--imkit-surface); height: 100%; overflow-y: auto; }
.imkit-list-empty { padding: 32px 16px; text-align: center; color: var(--imkit-text-muted); }
.imkit-list-searchbar { position: sticky; top: 0; z-index: 1; background: var(--imkit-surface); padding: 8px 10px 0; }
.imkit-list-header { padding: 10px 16px 4px; font-size: 12px; color: var(--imkit-text-muted); }
.imkit-search-hit mark { background: color-mix(in srgb, var(--imkit-accent) 28%, transparent); color: inherit; border-radius: 2px; }
.imkit-list-item { display: flex; gap: 12px; align-items: center; padding: 10px 14px; border: 0; border-bottom: 1px solid var(--imkit-border);
  background: transparent; color: inherit; text-align: left; cursor: pointer; font: inherit; width: 100%; }
.imkit-list-item:hover, .imkit-list-item[aria-current="true"] { background: color-mix(in srgb, var(--imkit-accent) 8%, transparent); }
.imkit-list-main { flex: 1; min-width: 0; }
.imkit-list-row { display: flex; justify-content: space-between; gap: 8px; }
.imkit-list-title { font-weight: 600; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.imkit-list-time { font-size: 12px; color: var(--imkit-text-muted); flex: none; }
.imkit-list-preview { font-size: 13px; color: var(--imkit-text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.imkit-list-draft { color: var(--imkit-danger); }
.imkit-badge { min-width: 20px; height: 20px; padding: 0 6px; border-radius: 10px; background: var(--imkit-danger); color: #fff;
  font-size: 12px; display: inline-flex; align-items: center; justify-content: center; flex: none; }
.imkit-badge-muted { background: var(--imkit-text-muted); }
.imkit-chat { display: flex; flex-direction: column; height: 100%; min-height: 0; background: var(--imkit-bg); }
.imkit-chat-header { display: flex; align-items: center; gap: 10px; padding: 10px 14px; background: var(--imkit-surface); border-bottom: 1px solid var(--imkit-border); }
.imkit-chat-title { font-weight: 600; }
.imkit-chat-subtitle { font-size: 12px; color: var(--imkit-text-muted); min-height: 15px; }
.imkit-icon-button { border: 0; background: transparent; color: inherit; font: inherit; cursor: pointer; padding: 6px 8px; border-radius: 8px; }
.imkit-icon-button:hover { background: color-mix(in srgb, var(--imkit-text) 8%, transparent); }
.imkit-messages { flex: 1; overflow-y: auto; padding: 12px 14px; display: flex; flex-direction: column; gap: 6px; min-height: 0; }
.imkit-divider { align-self: center; font-size: 12px; color: var(--imkit-text-muted); margin: 8px 0; }
.imkit-divider-new { align-self: stretch; text-align: center; border-top: 1px solid var(--imkit-accent); color: var(--imkit-accent); padding-top: 4px; }
.imkit-row { display: flex; gap: 8px; align-items: flex-end; max-width: 100%; }
.imkit-row-mine { flex-direction: row-reverse; }
.imkit-row-body { display: flex; flex-direction: column; max-width: min(72%, 560px); }
.imkit-row-mine .imkit-row-body { align-items: flex-end; }
.imkit-sender { font-size: 12px; color: var(--imkit-text-muted); margin: 0 4px 2px; }
.imkit-bubble { padding: 8px 12px; border-radius: var(--imkit-radius); background: var(--imkit-bubble-other); color: var(--imkit-bubble-other-text);
  white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.4; box-shadow: 0 1px 1px rgb(0 0 0 / 0.05); }
.imkit-row-mine .imkit-bubble { background: var(--imkit-bubble-mine); color: var(--imkit-bubble-mine-text); }
.imkit-bubble-failed { outline: 2px solid var(--imkit-danger); }
.imkit-status { font-size: 11px; color: var(--imkit-text-muted); margin: 2px 4px 0; border: 0; background: none; padding: 0; font: inherit; font-size: 11px; }
.imkit-status-failed { color: var(--imkit-danger); cursor: pointer; }
.imkit-forwarded { font-size: 11px; opacity: .75; display: block; }
.imkit-media { display: block; border-radius: var(--imkit-radius); object-fit: cover; background: color-mix(in srgb, var(--imkit-text) 10%, transparent); cursor: pointer; border: 0; }
.imkit-video-poster { position: relative; background-size: cover; background-position: center; color: #fff; }
.imkit-play { font-size: 28px; text-shadow: 0 1px 4px rgb(0 0 0 / .6); }
.imkit-video-duration { position: absolute; right: 6px; bottom: 4px; font-size: 12px; text-shadow: 0 1px 2px rgb(0 0 0 / .8); }
.imkit-voice { display: inline-flex; align-items: center; gap: 8px; border: 0; background: transparent; color: inherit; font: inherit; cursor: pointer; padding: 0; }
.imkit-row-mine .imkit-voice { flex-direction: row-reverse; }
.imkit-file { display: inline-flex; gap: 8px; align-items: center; color: inherit; }
.imkit-system { align-self: center; font-size: 12px; color: var(--imkit-text-muted); }
.imkit-composer { display: flex; gap: 8px; align-items: flex-end; padding: 8px 10px; background: var(--imkit-surface); border-top: 1px solid var(--imkit-border); }
.imkit-input { flex: 1; resize: none; border: 1px solid var(--imkit-border); border-radius: 18px; padding: 8px 12px; font: inherit;
  background: var(--imkit-bg); color: inherit; max-height: 140px; min-height: 38px; }
.imkit-send { border: 0; border-radius: 18px; padding: 8px 16px; background: var(--imkit-accent); color: var(--imkit-on-accent); font: inherit; cursor: pointer; }
.imkit-send:disabled { opacity: .5; cursor: default; }
.imkit-talk { flex: 1; border: 1px solid var(--imkit-border); border-radius: 18px; padding: 9px; background: var(--imkit-bg); color: inherit;
  font: inherit; cursor: pointer; user-select: none; touch-action: none; }
.imkit-talk-active { background: color-mix(in srgb, var(--imkit-accent) 18%, var(--imkit-bg)); }
.imkit-talk-cancel { background: color-mix(in srgb, var(--imkit-danger) 18%, var(--imkit-bg)); }
.imkit-overlay { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; pointer-events: none; }
.imkit-overlay-card { background: rgb(0 0 0 / .72); color: #fff; padding: 16px 22px; border-radius: 14px; text-align: center; min-width: 160px; }
.imkit-overlay-cancel { background: color-mix(in srgb, var(--imkit-danger) 85%, black); }
.imkit-menu { position: fixed; z-index: 1000; background: var(--imkit-surface); border: 1px solid var(--imkit-border); border-radius: 10px;
  box-shadow: 0 8px 24px rgb(0 0 0 / .18); padding: 4px; display: flex; flex-direction: column; min-width: 140px; }
.imkit-menu button { text-align: left; border: 0; background: transparent; color: inherit; padding: 8px 12px; border-radius: 6px; font: inherit; cursor: pointer; }
.imkit-menu button:hover { background: color-mix(in srgb, var(--imkit-text) 8%, transparent); }
.imkit-toast { position: absolute; left: 50%; bottom: 72px; transform: translateX(-50%); background: rgb(0 0 0 / .8); color: #fff;
  padding: 8px 14px; border-radius: 8px; font-size: 13px; }
.imkit-viewer { position: fixed; inset: 0; z-index: 1000; background: rgb(0 0 0 / .9); display: flex; align-items: center; justify-content: center; }
.imkit-viewer img { max-width: 96vw; max-height: 96vh; }
.imkit-info { display: flex; flex-direction: column; height: 100%; min-height: 0; background: var(--imkit-bg); }
.imkit-info-body { flex: 1; overflow-y: auto; padding-bottom: 24px; }
.imkit-info-head { display: flex; align-items: center; gap: 12px; padding: 16px; background: var(--imkit-surface); margin-bottom: 8px; }
.imkit-info-title { font-size: 18px; font-weight: 600; overflow-wrap: anywhere; }
.imkit-info-caption { font-size: 13px; color: var(--imkit-text-muted); padding: 8px 16px 4px; }
.imkit-info-group { background: var(--imkit-surface); margin-bottom: 8px; display: flex; flex-direction: column; }
.imkit-info-row { display: flex; align-items: center; gap: 12px; padding: 10px 16px; min-height: 48px; border: 0; border-bottom: 1px solid var(--imkit-border);
  background: transparent; color: inherit; font: inherit; text-align: left; width: 100%; }
.imkit-info-group > .imkit-info-row:last-child { border-bottom: 0; }
button.imkit-info-row, label.imkit-info-row { cursor: pointer; }
button.imkit-info-row:hover, label.imkit-info-row:hover { background: color-mix(in srgb, var(--imkit-text) 5%, transparent); }
label.imkit-info-row[aria-disabled="true"] { cursor: default; opacity: .55; }
.imkit-info-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.imkit-info-accent { color: var(--imkit-accent); }
.imkit-info-plus { width: 40px; text-align: center; font-size: 20px; }
.imkit-info-hint { font-size: 12px; color: var(--imkit-text-muted); margin: 0; padding: 4px 16px 12px; }
.imkit-role { font-size: 11px; color: var(--imkit-accent); background: color-mix(in srgb, var(--imkit-accent) 12%, transparent); padding: 2px 6px; border-radius: 4px; flex: none; }
.imkit-toggle input { width: 36px; height: 20px; accent-color: var(--imkit-accent); }
.imkit-danger-text { color: var(--imkit-danger) !important; }
.imkit-dialog { position: fixed; inset: 0; z-index: 900; background: rgb(0 0 0 / .4); display: flex; align-items: center; justify-content: center; padding: 16px; }
.imkit-dialog-card { background: var(--imkit-surface); color: var(--imkit-text); border-radius: 12px; padding: 18px; width: min(420px, 100%);
  max-height: calc(100vh - 32px); overflow-y: auto; box-shadow: 0 12px 32px rgb(0 0 0 / .25); }
.imkit-dialog-title { margin: 0 0 12px; font-size: 17px; }
.imkit-dialog-text { margin: 0 0 16px; line-height: 1.5; }
.imkit-dialog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 12px; }
.imkit-text-button { border: 0; background: transparent; color: var(--imkit-accent); font: inherit; padding: 8px 12px; border-radius: 8px; cursor: pointer; }
.imkit-text-button:hover { background: color-mix(in srgb, var(--imkit-accent) 10%, transparent); }
.imkit-text-button:disabled { opacity: .5; cursor: default; }
.imkit-field { width: 100%; border: 1px solid var(--imkit-border); border-radius: 8px; padding: 8px 12px; font: inherit; background: var(--imkit-bg); color: inherit; margin-bottom: 8px; }
.imkit-pick-list { max-height: 50vh; overflow-y: auto; border: 1px solid var(--imkit-border); border-radius: 8px; }
.imkit-pick-list .imkit-info-row { min-height: 52px; }
.imkit-composer-wrap { background: var(--imkit-surface); }
.imkit-reply-bar { display: flex; align-items: center; gap: 8px; padding: 6px 10px 0 14px; border-top: 1px solid var(--imkit-border); }
.imkit-reply-name { font-size: 12px; color: var(--imkit-accent); }
.imkit-reply-text, .imkit-quote-text { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.imkit-reply-bar + .imkit-composer { border-top: 0; }
.imkit-quote { display: flex; flex-direction: column; border-left: 3px solid currentColor; padding: 2px 8px; margin-bottom: 6px; border-radius: 4px;
  background: color-mix(in srgb, currentColor 8%, transparent); opacity: .85; max-width: 100%; }
.imkit-quote-name { font-size: 11px; font-weight: 600; }
.imkit-row-check { align-self: center; flex: none; width: 18px; height: 18px; accent-color: var(--imkit-accent); }
.imkit-row-selectable { cursor: pointer; }
.imkit-row-selected .imkit-bubble, .imkit-row-selected .imkit-card { outline: 2px solid var(--imkit-accent); outline-offset: 1px; }
/* The check box stays on the left edge, also next to own messages (their row runs right to left). */
.imkit-row-mine .imkit-row-check { order: 99; margin-right: auto; }
.imkit-list-item-pinned { background: color-mix(in srgb, var(--imkit-text) 4%, transparent); }
.imkit-list-muted { font-size: 12px; opacity: .6; flex: none; }
.imkit-readers { display: inline-flex; align-items: center; gap: 4px; cursor: pointer; }
.imkit-readers-faces { display: inline-flex; }
.imkit-readers-faces > .imkit-avatar { border: 1.5px solid var(--imkit-bg); margin-left: -5px; font-size: 8px; }
.imkit-readers-faces > .imkit-avatar:first-child { margin-left: 0; }
.imkit-readers-more { font-size: 9px; margin-left: 2px; }
.imkit-emoji-panel { display: grid; grid-template-columns: repeat(auto-fill, minmax(36px, 1fr)); gap: 2px; padding: 6px 10px 10px; max-height: 196px;
  overflow-y: auto; background: var(--imkit-surface); border-top: 1px solid var(--imkit-border); }
.imkit-emoji-panel button { border: 0; background: transparent; font-size: 22px; line-height: 1; padding: 6px 0; border-radius: 6px; cursor: pointer; }
.imkit-emoji-panel button:hover { background: color-mix(in srgb, var(--imkit-text) 8%, transparent); }
.imkit-contacts { display: flex; flex-direction: column; min-height: 0; }
.imkit-contacts > .imkit-field { margin: 8px 10px; width: auto; }
.imkit-contact-list { overflow-y: auto; min-height: 0; }
.imkit-system-row { align-self: stretch; display: flex; justify-content: center; }
`;
