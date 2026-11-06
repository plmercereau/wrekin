const SESSION_ID = /^session_[A-Za-z0-9]{8,}$/;

/**
 * The Claude Code Cloud session id a teleport needs, from the id itself or from the
 * session's URL (`https://claude.ai/code/session_01CN…`, possibly with a query or a
 * trailing path). A chat URL (`/chat/<uuid>`) or anything else gives null: Claude must
 * pass its own Claude Code Cloud session, not the conversation.
 */
export function parseCloudSessionId(input: string): string | null {
  const value = input.trim();
  if (!value) return null;
  if (SESSION_ID.test(value)) return value;
  const candidate = /^[a-z]+:\/\//i.test(value) ? value : `https://${value}`;
  let url: URL;
  try {
    url = new URL(candidate);
  } catch {
    return null;
  }
  for (const segment of url.pathname.split("/")) {
    if (SESSION_ID.test(segment)) return segment;
  }
  return null;
}
