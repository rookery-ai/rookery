import { describe, expect, test } from "vitest";
import { statusLabel, type MCPServer } from "./mcp";

describe("statusLabel", () => {
  // The two failure states must stay visually distinct, because they call for
  // different actions from the owner: a rejected credential only they can fix,
  // while an unreachable server may well come back on its own. Collapsing them
  // into one "broken" badge would push someone to re-enter a working token.
  test("distinguishes a rejected credential from an unreachable server", () => {
    const auth = statusLabel("NEEDS_AUTH");
    const unreachable = statusLabel("UNREACHABLE");
    expect(auth.text).not.toBe(unreachable.text);
    expect(auth.tone).toBe("danger");
    expect(unreachable.tone).toBe("warn");
  });

  test("an active server reads as connected", () => {
    expect(statusLabel("ACTIVE")).toEqual({ text: "Connected", tone: "ok" });
  });
});

describe("MCPServer shape", () => {
  // A credential must never round-trip to the browser, not even redacted: a field
  // that exists is a field a future handler can populate. has_token carries the
  // only thing the UI needs — whether one is stored — so the edit form can offer
  // "leave blank to keep it".
  test("carries no credential field", () => {
    const server: MCPServer = {
      id: "s1",
      name: "Home Assistant",
      slug: "home_assistant",
      url: "http://192.168.1.10:3000/mcp",
      auth_kind: "bearer",
      header_name: "",
      has_token: true,
      enabled: true,
      status: "ACTIVE",
      last_error: "",
      synced_at: "2026-08-11 10:00:00",
      tool_count: 12,
      active_tools: 12,
    };
    expect(Object.keys(server)).not.toContain("token");
    expect(Object.keys(server)).not.toContain("encrypted_token");
    expect(server.has_token).toBe(true);
  });
});
