import { describe, expect, it } from "vitest";
import { loginRedirect, safeRedirect } from "../../src/session-guard";

describe("safeRedirect", () => {
  it("keeps a same-origin path, query string included", () => {
    for (const path of ["/nodes", "/device?user_code=ABCD-EFGH", "/consent?client_id=x&sig=y", "/", "/nodes#top"]) {
      expect(safeRedirect(path, "/fallback"), path).toBe(path);
    }
  });

  it("falls back on anything that could leave the origin", () => {
    const rejected = [
      "/\\evil.com", // WHATWG parses the backslash as a slash: scheme-relative
      "//evil.com",
      "/\\/evil.com",
      "/%5c%5cevil.com",
      "/%2F%2Fevil.com",
      "https://evil.com",
      "http://evil.com/nodes",
      "javascript:alert(1)",
      "/nodes\nSet-Cookie: x=y",
      "/nodes\u0000",
      "nodes",
      "",
    ];
    for (const value of rejected) {
      expect(safeRedirect(value, "/nodes"), JSON.stringify(value)).toBe("/nodes");
    }
  });

  it("falls back when there is no value at all", () => {
    expect(safeRedirect(null, "/nodes")).toBe("/nodes");
    expect(safeRedirect(undefined, "/")).toBe("/");
  });

  it("round-trips through loginRedirect", () => {
    const target = "/device?user_code=ABCD-EFGH";
    const back = new URL(loginRedirect(target), "https://wrekin.invalid").searchParams.get("redirect");
    expect(safeRedirect(back, "/nodes")).toBe(target);
  });
});
