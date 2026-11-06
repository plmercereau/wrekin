import { useEffect, useRef, useState } from "react";
import { LIVE_PATH, LIVE_PING_MS, livePingFrame, NODES_CHANGED } from "../live";

/** Hints closer than this are one refetch. */
const DEBOUNCE_MS = 250;
const MAX_RETRY_MS = 30_000;

/**
 * Opens the live channel (src/live.ts) for as long as the component is mounted and calls
 * `onChange` whenever the hub says the account's nodes changed, and after a reconnection,
 * since hints sent while the socket was down are lost. Reconnects with exponential back-off.
 * Returns whether the channel is open. Browser only: the server render returns false.
 */
export function useLiveNodes(onChange: () => unknown): boolean {
  const [live, setLive] = useState(false);
  const callback = useRef(onChange);
  callback.current = onChange;

  useEffect(() => {
    let socket: WebSocket | null = null;
    let stopped = false;
    let attempts = 0;
    let opened = false;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let debounceTimer: ReturnType<typeof setTimeout> | undefined;
    let pingTimer: ReturnType<typeof setInterval> | undefined;

    const changed = () => {
      clearTimeout(debounceTimer);
      debounceTimer = setTimeout(() => void callback.current(), DEBOUNCE_MS);
    };

    const connect = () => {
      const url = new URL(LIVE_PATH, window.location.href);
      url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
      const ws = new WebSocket(url);
      socket = ws;
      ws.onopen = () => {
        attempts = 0;
        setLive(true);
        if (opened) changed();
        opened = true;
        pingTimer = setInterval(() => ws.readyState === WebSocket.OPEN && ws.send(livePingFrame), LIVE_PING_MS);
      };
      ws.onmessage = (event) => {
        try {
          if ((JSON.parse(String(event.data)) as { type?: unknown }).type === NODES_CHANGED) changed();
        } catch {
          // not a frame of ours
        }
      };
      ws.onclose = () => {
        clearInterval(pingTimer);
        setLive(false);
        if (stopped) return;
        retryTimer = setTimeout(connect, Math.min(MAX_RETRY_MS, 1000 * 2 ** attempts++));
      };
    };

    connect();
    return () => {
      stopped = true;
      clearTimeout(retryTimer);
      clearTimeout(debounceTimer);
      clearInterval(pingTimer);
      socket?.close();
    };
  }, []);

  return live;
}
