import { Link } from "@tanstack/react-router";

/** A plain error page in the shared layout. */
export function ErrorPage({ title, message }: { title: string; message: string }) {
  return (
    <>
      <h1>{title}</h1>
      <p>{message}</p>
      <p>
        <Link to="/">Back to the front page</Link>
      </p>
    </>
  );
}

/** The line under a page's buttons: progress, or the reason a call failed. */
export function Status({ text, error = false }: { text: string | null; error?: boolean }) {
  return <p className={error ? "status error" : "status"}>{text ?? ""}</p>;
}

/** `2026-11-06T00:00:00.000Z` → `2026-11-06 00:00:00 UTC`; null → `never`. */
export const formatTime = (iso: string | null): string =>
  iso ? iso.replace("T", " ").replace(/\.\d+Z$/, " UTC").replace(/Z$/, " UTC") : "never";
