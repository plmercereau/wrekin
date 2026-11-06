/**
 * The nodes page's live channel: a WebSocket from the browser to the account's hub that
 * carries one kind of frame, a hint that the account's nodes changed (one authenticated,
 * disconnected, was registered or removed). The hint carries no data; the page fetches the
 * list again through `nodes.list`, so what it shows is always what the API says.
 */
export const LIVE_PATH = "/api/nodes/live";

/** The only frame the hub sends on the live channel. */
export const NODES_CHANGED = "nodes.changed";
export const nodesChangedFrame = JSON.stringify({ v: 1, type: NODES_CHANGED });

/** A keep-alive the runtime answers without waking the hub (the nodes' own ping, see account-hub.ts). */
export const livePingFrame = '{"v":1,"type":"ping"}';
export const LIVE_PING_MS = 30_000;
