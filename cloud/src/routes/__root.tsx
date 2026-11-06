import { createRootRoute, HeadContent, Link, Outlet, Scripts } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { ErrorPage } from "../components";
import { getPageContext } from "../server/session";
import { css } from "../styles";

export const Route = createRootRoute({
  // The public origin and the session, once per navigation; every route reads them from its context.
  beforeLoad: () => getPageContext(),
  head: () => ({
    meta: [{ charSet: "utf-8" }, { name: "viewport", content: "width=device-width, initial-scale=1" }, { title: "Wrekin" }],
    styles: [{ children: css }],
  }),
  shellComponent: Shell,
  component: Layout,
  notFoundComponent: () => <ErrorPage title="Not found" message="There is nothing at this address." />,
  errorComponent: () => <ErrorPage title="Error" message="Something went wrong on the relay. Please try again." />,
});

/** The document: the stylesheet is inlined through `head`, nothing comes from a CDN. */
function Shell({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <head>
        <HeadContent />
      </head>
      <body>
        {children}
        <Scripts />
      </body>
    </html>
  );
}

/** One layout for every page: mobile-first (the device page is opened from a phone), dark-mode aware. */
function Layout() {
  const { user } = Route.useRouteContext();
  return (
    <>
      <header>
        <Link to="/">Wrekin</Link>
        <nav>
          {user ? (
            <>
              <Link to="/nodes">Nodes</Link>
              <span className="muted"> · {user.name}</span>
            </>
          ) : (
            <Link to="/login">Sign in</Link>
          )}
        </nav>
      </header>
      <main>
        <Outlet />
      </main>
    </>
  );
}
