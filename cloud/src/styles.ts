/** One stylesheet for every page, inlined by the root route: mobile-first (the device page is opened from a phone), dark-mode aware, no external asset. */
export const css = `
:root { color-scheme: light dark; --bg: #fbfaf7; --fg: #1d1c1a; --muted: #6b6760; --line: #e4e0d8; --accent: #b4532a; --ok: #2f7d4f; --card: #ffffff; --danger: #a3302d; }
@media (prefers-color-scheme: dark) { :root { --bg: #161513; --fg: #ece8e1; --muted: #9b958c; --line: #2d2b27; --accent: #e0825a; --ok: #5cb57f; --card: #1f1d1a; --danger: #e06c69; } }
* { box-sizing: border-box; }
body { margin: 0; font: 16px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; background: var(--bg); color: var(--fg); }
header { display: flex; justify-content: space-between; align-items: center; padding: 14px 16px; border-bottom: 1px solid var(--line); }
header a { color: inherit; text-decoration: none; font-weight: 600; }
header nav a { font-weight: 400; margin-left: 14px; color: var(--muted); }
main { max-width: 640px; margin: 0 auto; padding: 24px 16px 48px; }
h1 { font-size: 1.6rem; margin: 0 0 12px; }
h2 { font-size: 1.1rem; margin: 28px 0 8px; }
p { margin: 8px 0; }
.muted { color: var(--muted); }
.card { background: var(--card); border: 1px solid var(--line); border-radius: 10px; padding: 16px; margin: 12px 0; }
.row { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; }
.code { font: 1.9rem/1.2 ui-monospace, SFMono-Regular, Menlo, monospace; letter-spacing: 0.18em; text-align: center; padding: 12px; border: 1px dashed var(--line); border-radius: 8px; }
pre, code { font: 0.92rem ui-monospace, SFMono-Regular, Menlo, monospace; }
pre { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 12px; overflow-x: auto; }
.actions { display: flex; gap: 10px; margin-top: 16px; flex-wrap: wrap; }
button, .button { appearance: none; border: 1px solid var(--line); background: var(--card); color: var(--fg); border-radius: 8px; padding: 12px 18px; font: inherit; cursor: pointer; flex: 1 1 140px; text-align: center; text-decoration: none; }
button.primary, .button.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
button.danger { color: var(--danger); }
button:disabled { opacity: 0.6; cursor: default; }
.badge { display: inline-block; font-size: 0.8rem; padding: 2px 8px; border-radius: 999px; border: 1px solid var(--line); color: var(--muted); }
.badge.online { color: var(--ok); border-color: var(--ok); }
.fingerprint { font: 0.85rem ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--muted); }
.status { margin-top: 14px; min-height: 1.5em; }
.status.error { color: var(--danger); }
.code-input { width: 100%; font: 1.4rem ui-monospace, monospace; letter-spacing: 0.15em; padding: 10px; margin: 8px 0 14px; border: 1px solid var(--line); border-radius: 8px; background: transparent; color: inherit; }
ol { padding-left: 20px; }
`;
