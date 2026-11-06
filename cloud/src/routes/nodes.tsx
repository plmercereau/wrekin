import { useMutation } from "@tanstack/react-query";
import { createFileRoute, useRouter } from "@tanstack/react-router";
import { useTRPC } from "../client/trpc";
import { formatTime, Status } from "../components";
import { listMyNodes } from "../server/pages";
import { requireUser } from "../session-guard";

export const Route = createFileRoute("/nodes")({
  beforeLoad: ({ context, location }) => ({ user: requireUser(context.user, location.href) }),
  loader: () => listMyNodes(),
  head: () => ({ meta: [{ title: "Your nodes · Wrekin" }] }),
  component: Nodes,
});

/** The account's machines, with their fingerprint and connection state. */
function Nodes() {
  const nodes = Route.useLoaderData();
  const router = useRouter();
  const trpc = useTRPC();
  const remove = useMutation(trpc.nodes.remove.mutationOptions({ onSuccess: () => router.invalidate() }));

  const confirmRemove = (id: string, name: string) => {
    if (!window.confirm(`Remove ${name}? The agent on it will have to pair again.`)) return;
    remove.mutate({ id });
  };

  return (
    <>
      <h1>Your nodes</h1>
      {nodes.length === 0 ? (
        <div className="card">
          <p>No machine is paired yet.</p>
          <p className="muted">
            Run <code>wrekin auth</code> on a machine and approve the code it prints.
          </p>
        </div>
      ) : (
        nodes.map((node) => (
          <div className="card" key={node.id}>
            <div className="row">
              <strong>{node.name}</strong>
              <span className={node.online ? "badge online" : "badge"}>{node.online ? "online" : "offline"}</span>
            </div>
            <p className="fingerprint">{node.fingerprint}</p>
            <p className="muted">
              {node.online ? `Connected since ${formatTime(node.connectedAt)}` : `Last seen ${formatTime(node.lastSeenAt)}`}
              {node.agentVersion ? ` · agent ${node.agentVersion}` : ""}
              {node.platform ? ` · ${node.platform}` : ""}
            </p>
            {node.harnesses.length ? (
              <p className="muted">
                Harnesses:{" "}
                {node.harnesses.map((h) => `${h.id}${h.available ? "" : " (unavailable)"}${h.version ? ` ${h.version}` : ""}`).join(", ")}
              </p>
            ) : null}
            <div className="actions">
              <button className="danger" data-remove={node.id} disabled={remove.isPending} onClick={() => confirmRemove(node.id, node.name)}>
                Remove
              </button>
            </div>
          </div>
        ))
      )}
      <Status text={remove.error ? `Could not remove the node: ${remove.error.message}` : null} error={remove.error !== null} />
      <p className="muted">
        To add a machine: <code>wrekin auth</code> there, then approve the code on <a href="/device">the pairing page</a>.
      </p>
    </>
  );
}
