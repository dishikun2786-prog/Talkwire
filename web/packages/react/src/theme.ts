/**
 * The look of the components, all in CSS variables on `.talkwire-root` (5.9).
 * An app themes the kit by setting the variables on any ancestor, e.g.
 *
 *     .my-app { --talkwire-accent: #e91e63; --talkwire-radius: 6px; }
 *
 * Dark colours apply with `prefers-color-scheme: dark` unless the root has
 * `data-theme="light"`; `data-theme="dark"` on the root forces them.
 */

export const talkwireCss = `
.talkwire-root {
  --talkwire-font: system-ui, -apple-system, "Segoe UI", Roboto, "Noto Sans SC", sans-serif;
  --talkwire-bg: #f3f4f6;
  --talkwire-surface: #ffffff;
  --talkwire-text: #111827;
  --talkwire-text-muted: #6b7280;
  --talkwire-border: #e5e7eb;
  --talkwire-accent: #2563eb;
  --talkwire-on-accent: #ffffff;
  --talkwire-bubble-mine: #2563eb;
  --talkwire-bubble-mine-text: #ffffff;
  --talkwire-bubble-other: #ffffff;
  --talkwire-bubble-other-text: #111827;
  --talkwire-danger: #dc2626;
  --talkwire-online: #16a34a;
  --talkwire-radius: 14px;
  font-family: var(--talkwire-font);
  color: var(--talkwire-text);
  background: var(--talkwire-bg);
  box-sizing: border-box;
}
.talkwire-root *, .talkwire-root *::before, .talkwire-root *::after { box-sizing: inherit; }
@media (prefers-color-scheme: dark) {
  .talkwire-root:not([data-theme="light"]) {
    --talkwire-bg: #0b0f17; --talkwire-surface: #151b26; --talkwire-text: #e5e7eb; --talkwire-text-muted: #9ca3af;
    --talkwire-border: #273041; --talkwire-bubble-other: #1f2937; --talkwire-bubble-other-text: #e5e7eb;
    --talkwire-accent: #3b82f6; --talkwire-bubble-mine: #2563eb;
  }
}
.talkwire-root[data-theme="dark"] {
  --talkwire-bg: #0b0f17; --talkwire-surface: #151b26; --talkwire-text: #e5e7eb; --talkwire-text-muted: #9ca3af;
  --talkwire-border: #273041; --talkwire-bubble-other: #1f2937; --talkwire-bubble-other-text: #e5e7eb;
  --talkwire-accent: #3b82f6; --talkwire-bubble-mine: #2563eb;
}
.talkwire-avatar { flex: none; border-radius: 50%; background: var(--talkwire-accent); color: var(--talkwire-on-accent);
  display: inline-flex; align-items: center; justify-content: center; font-weight: 600; overflow: hidden; position: relative; }
.talkwire-avatar img { width: 100%; height: 100%; object-fit: cover; }
.talkwire-dot { position: absolute; right: 0; bottom: 0; width: 28%; height: 28%; border-radius: 50%;
  background: var(--talkwire-online); border: 2px solid var(--talkwire-surface); }
.talkwire-banner { padding: 6px 12px; font-size: 13px; text-align: center; background: #fef3c7; color: #92400e; }
.talkwire-list { display: flex; flex-direction: column; background: var(--talkwire-surface); height: 100%; overflow-y: auto; }
.talkwire-list-empty { padding: 32px 16px; text-align: center; color: var(--talkwire-text-muted); }
.talkwire-list-searchbar { position: sticky; top: 0; z-index: 1; background: var(--talkwire-surface); padding: 8px 10px 0; }
.talkwire-list-header { padding: 10px 16px 4px; font-size: 12px; color: var(--talkwire-text-muted); }
.talkwire-search-hit mark { background: color-mix(in srgb, var(--talkwire-accent) 28%, transparent); color: inherit; border-radius: 2px; }
.talkwire-list-item { display: flex; gap: 12px; align-items: center; padding: 10px 14px; border: 0; border-bottom: 1px solid var(--talkwire-border);
  background: transparent; color: inherit; text-align: left; cursor: pointer; font: inherit; width: 100%; }
.talkwire-list-item:hover, .talkwire-list-item[aria-current="true"] { background: color-mix(in srgb, var(--talkwire-accent) 8%, transparent); }
.talkwire-list-main { flex: 1; min-width: 0; }
.talkwire-list-row { display: flex; justify-content: space-between; gap: 8px; }
.talkwire-list-title { font-weight: 600; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.talkwire-list-time { font-size: 12px; color: var(--talkwire-text-muted); flex: none; }
.talkwire-list-preview { font-size: 13px; color: var(--talkwire-text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.talkwire-list-draft { color: var(--talkwire-danger); }
.talkwire-badge { min-width: 20px; height: 20px; padding: 0 6px; border-radius: 10px; background: var(--talkwire-danger); color: #fff;
  font-size: 12px; display: inline-flex; align-items: center; justify-content: center; flex: none; }
.talkwire-badge-muted { background: var(--talkwire-text-muted); }
.talkwire-chat { display: flex; flex-direction: column; height: 100%; min-height: 0; background: var(--talkwire-bg); }
.talkwire-chat-header { display: flex; align-items: center; gap: 10px; padding: 10px 14px; background: var(--talkwire-surface); border-bottom: 1px solid var(--talkwire-border); }
.talkwire-chat-title { font-weight: 600; }
.talkwire-chat-subtitle { font-size: 12px; color: var(--talkwire-text-muted); min-height: 15px; }
.talkwire-icon-button { border: 0; background: transparent; color: inherit; font: inherit; cursor: pointer; padding: 6px 8px; border-radius: 8px; }
.talkwire-icon-button:hover { background: color-mix(in srgb, var(--talkwire-text) 8%, transparent); }
.talkwire-messages { flex: 1; overflow-y: auto; padding: 12px 14px; display: flex; flex-direction: column; gap: 6px; min-height: 0; }
.talkwire-divider { align-self: center; font-size: 12px; color: var(--talkwire-text-muted); margin: 8px 0; }
.talkwire-divider-new { align-self: stretch; text-align: center; border-top: 1px solid var(--talkwire-accent); color: var(--talkwire-accent); padding-top: 4px; }
.talkwire-row { display: flex; gap: 8px; align-items: flex-end; max-width: 100%; }
.talkwire-row-mine { flex-direction: row-reverse; }
.talkwire-row-body { display: flex; flex-direction: column; max-width: min(72%, 560px); }
.talkwire-row-mine .talkwire-row-body { align-items: flex-end; }
.talkwire-sender { font-size: 12px; color: var(--talkwire-text-muted); margin: 0 4px 2px; }
.talkwire-bubble { padding: 8px 12px; border-radius: var(--talkwire-radius); background: var(--talkwire-bubble-other); color: var(--talkwire-bubble-other-text);
  white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.4; box-shadow: 0 1px 1px rgb(0 0 0 / 0.05); }
.talkwire-row-mine .talkwire-bubble { background: var(--talkwire-bubble-mine); color: var(--talkwire-bubble-mine-text); }
.talkwire-bubble-failed { outline: 2px solid var(--talkwire-danger); }
.talkwire-status { font-size: 11px; color: var(--talkwire-text-muted); margin: 2px 4px 0; border: 0; background: none; padding: 0; font: inherit; font-size: 11px; }
.talkwire-status-failed { color: var(--talkwire-danger); cursor: pointer; }
.talkwire-forwarded { font-size: 11px; opacity: .75; display: block; }
.talkwire-media { display: block; border-radius: var(--talkwire-radius); object-fit: cover; background: color-mix(in srgb, var(--talkwire-text) 10%, transparent); cursor: pointer; border: 0; }
.talkwire-video-poster { position: relative; background-size: cover; background-position: center; color: #fff; }
.talkwire-play { font-size: 28px; text-shadow: 0 1px 4px rgb(0 0 0 / .6); }
.talkwire-video-duration { position: absolute; right: 6px; bottom: 4px; font-size: 12px; text-shadow: 0 1px 2px rgb(0 0 0 / .8); }
.talkwire-voice { display: inline-flex; align-items: center; gap: 8px; border: 0; background: transparent; color: inherit; font: inherit; cursor: pointer; padding: 0; }
.talkwire-row-mine .talkwire-voice { flex-direction: row-reverse; }
.talkwire-file { display: inline-flex; gap: 8px; align-items: center; color: inherit; }
.talkwire-system { align-self: center; font-size: 12px; color: var(--talkwire-text-muted); }
.talkwire-composer { display: flex; gap: 8px; align-items: flex-end; padding: 8px 10px; background: var(--talkwire-surface); border-top: 1px solid var(--talkwire-border); }
.talkwire-input { flex: 1; resize: none; border: 1px solid var(--talkwire-border); border-radius: 18px; padding: 8px 12px; font: inherit;
  background: var(--talkwire-bg); color: inherit; max-height: 140px; min-height: 38px; }
.talkwire-send { border: 0; border-radius: 18px; padding: 8px 16px; background: var(--talkwire-accent); color: var(--talkwire-on-accent); font: inherit; cursor: pointer; }
.talkwire-send:disabled { opacity: .5; cursor: default; }
.talkwire-talk { flex: 1; border: 1px solid var(--talkwire-border); border-radius: 18px; padding: 9px; background: var(--talkwire-bg); color: inherit;
  font: inherit; cursor: pointer; user-select: none; touch-action: none; }
.talkwire-talk-active { background: color-mix(in srgb, var(--talkwire-accent) 18%, var(--talkwire-bg)); }
.talkwire-talk-cancel { background: color-mix(in srgb, var(--talkwire-danger) 18%, var(--talkwire-bg)); }
.talkwire-overlay { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; pointer-events: none; }
.talkwire-overlay-card { background: rgb(0 0 0 / .72); color: #fff; padding: 16px 22px; border-radius: 14px; text-align: center; min-width: 160px; }
.talkwire-overlay-cancel { background: color-mix(in srgb, var(--talkwire-danger) 85%, black); }
.talkwire-menu { position: fixed; z-index: 1000; background: var(--talkwire-surface); border: 1px solid var(--talkwire-border); border-radius: 10px;
  box-shadow: 0 8px 24px rgb(0 0 0 / .18); padding: 4px; display: flex; flex-direction: column; min-width: 140px; }
.talkwire-menu button { text-align: left; border: 0; background: transparent; color: inherit; padding: 8px 12px; border-radius: 6px; font: inherit; cursor: pointer; }
.talkwire-menu button:hover { background: color-mix(in srgb, var(--talkwire-text) 8%, transparent); }
.talkwire-toast { position: absolute; left: 50%; bottom: 72px; transform: translateX(-50%); background: rgb(0 0 0 / .8); color: #fff;
  padding: 8px 14px; border-radius: 8px; font-size: 13px; }
.talkwire-viewer { position: fixed; inset: 0; z-index: 1000; background: rgb(0 0 0 / .9); display: flex; align-items: center; justify-content: center; }
.talkwire-viewer img { max-width: 96vw; max-height: 96vh; }
.talkwire-info { display: flex; flex-direction: column; height: 100%; min-height: 0; background: var(--talkwire-bg); }
.talkwire-info-body { flex: 1; overflow-y: auto; padding-bottom: 24px; }
.talkwire-info-head { display: flex; align-items: center; gap: 12px; padding: 16px; background: var(--talkwire-surface); margin-bottom: 8px; }
.talkwire-info-title { font-size: 18px; font-weight: 600; overflow-wrap: anywhere; }
.talkwire-info-caption { font-size: 13px; color: var(--talkwire-text-muted); padding: 8px 16px 4px; }
.talkwire-info-group { background: var(--talkwire-surface); margin-bottom: 8px; display: flex; flex-direction: column; }
.talkwire-info-row { display: flex; align-items: center; gap: 12px; padding: 10px 16px; min-height: 48px; border: 0; border-bottom: 1px solid var(--talkwire-border);
  background: transparent; color: inherit; font: inherit; text-align: left; width: 100%; }
.talkwire-info-group > .talkwire-info-row:last-child { border-bottom: 0; }
button.talkwire-info-row, label.talkwire-info-row { cursor: pointer; }
button.talkwire-info-row:hover, label.talkwire-info-row:hover { background: color-mix(in srgb, var(--talkwire-text) 5%, transparent); }
label.talkwire-info-row[aria-disabled="true"] { cursor: default; opacity: .55; }
.talkwire-info-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.talkwire-info-accent { color: var(--talkwire-accent); }
.talkwire-info-plus { width: 40px; text-align: center; font-size: 20px; }
.talkwire-info-hint { font-size: 12px; color: var(--talkwire-text-muted); margin: 0; padding: 4px 16px 12px; }
.talkwire-role { font-size: 11px; color: var(--talkwire-accent); background: color-mix(in srgb, var(--talkwire-accent) 12%, transparent); padding: 2px 6px; border-radius: 4px; flex: none; }
.talkwire-toggle input { width: 36px; height: 20px; accent-color: var(--talkwire-accent); }
.talkwire-danger-text { color: var(--talkwire-danger) !important; }
.talkwire-dialog { position: fixed; inset: 0; z-index: 900; background: rgb(0 0 0 / .4); display: flex; align-items: center; justify-content: center; padding: 16px; }
.talkwire-dialog-card { background: var(--talkwire-surface); color: var(--talkwire-text); border-radius: 12px; padding: 18px; width: min(420px, 100%);
  max-height: calc(100vh - 32px); overflow-y: auto; box-shadow: 0 12px 32px rgb(0 0 0 / .25); }
.talkwire-dialog-title { margin: 0 0 12px; font-size: 17px; }
.talkwire-dialog-text { margin: 0 0 16px; line-height: 1.5; }
.talkwire-dialog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 12px; }
.talkwire-text-button { border: 0; background: transparent; color: var(--talkwire-accent); font: inherit; padding: 8px 12px; border-radius: 8px; cursor: pointer; }
.talkwire-text-button:hover { background: color-mix(in srgb, var(--talkwire-accent) 10%, transparent); }
.talkwire-text-button:disabled { opacity: .5; cursor: default; }
.talkwire-field { width: 100%; border: 1px solid var(--talkwire-border); border-radius: 8px; padding: 8px 12px; font: inherit; background: var(--talkwire-bg); color: inherit; margin-bottom: 8px; }
.talkwire-pick-list { max-height: 50vh; overflow-y: auto; border: 1px solid var(--talkwire-border); border-radius: 8px; }
.talkwire-pick-list .talkwire-info-row { min-height: 52px; }
.talkwire-composer-wrap { background: var(--talkwire-surface); }
.talkwire-reply-bar { display: flex; align-items: center; gap: 8px; padding: 6px 10px 0 14px; border-top: 1px solid var(--talkwire-border); }
.talkwire-reply-name { font-size: 12px; color: var(--talkwire-accent); }
.talkwire-reply-text, .talkwire-quote-text { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.talkwire-reply-bar + .talkwire-composer { border-top: 0; }
.talkwire-quote { display: flex; flex-direction: column; border-left: 3px solid currentColor; padding: 2px 8px; margin-bottom: 6px; border-radius: 4px;
  background: color-mix(in srgb, currentColor 8%, transparent); opacity: .85; max-width: 100%; }
.talkwire-quote-name { font-size: 11px; font-weight: 600; }
.talkwire-row-check { align-self: center; flex: none; width: 18px; height: 18px; accent-color: var(--talkwire-accent); }
.talkwire-row-selectable { cursor: pointer; }
.talkwire-row-selected .talkwire-bubble, .talkwire-row-selected .talkwire-card { outline: 2px solid var(--talkwire-accent); outline-offset: 1px; }
/* The check box stays on the left edge, also next to own messages (their row runs right to left). */
.talkwire-row-mine .talkwire-row-check { order: 99; margin-right: auto; }
.talkwire-list-item-pinned { background: color-mix(in srgb, var(--talkwire-text) 4%, transparent); }
.talkwire-list-muted { font-size: 12px; opacity: .6; flex: none; }
.talkwire-readers { display: inline-flex; align-items: center; gap: 4px; cursor: pointer; }
.talkwire-readers-faces { display: inline-flex; }
.talkwire-readers-faces > .talkwire-avatar { border: 1.5px solid var(--talkwire-bg); margin-left: -5px; font-size: 8px; }
.talkwire-readers-faces > .talkwire-avatar:first-child { margin-left: 0; }
.talkwire-readers-more { font-size: 9px; margin-left: 2px; }
.talkwire-emoji-panel { display: grid; grid-template-columns: repeat(auto-fill, minmax(36px, 1fr)); gap: 2px; padding: 6px 10px 10px; max-height: 196px;
  overflow-y: auto; background: var(--talkwire-surface); border-top: 1px solid var(--talkwire-border); }
.talkwire-emoji-panel button { border: 0; background: transparent; font-size: 22px; line-height: 1; padding: 6px 0; border-radius: 6px; cursor: pointer; }
.talkwire-emoji-panel button:hover { background: color-mix(in srgb, var(--talkwire-text) 8%, transparent); }
.talkwire-contacts { display: flex; flex-direction: column; min-height: 0; }
.talkwire-contacts > .talkwire-field { margin: 8px 10px; width: auto; }
.talkwire-contact-list { overflow-y: auto; min-height: 0; }
.talkwire-system-row { align-self: stretch; display: flex; justify-content: center; }
`;
