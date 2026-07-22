import { describe, it, expect, beforeEach } from "vitest";
import { readRecent, pushRecent, recentStorageKey, type RecentFile } from "./useRecentFiles";

const f = (path: string, title = path): RecentFile => ({ path, title });

const WS = "w1";

beforeEach(() => {
  localStorage.clear();
});

describe("pushRecent", () => {
  it("puts the newest entry first", () => {
    const list = pushRecent(pushRecent([], f("a.md")), f("b.md"));
    expect(list.map((e) => e.path)).toEqual(["b.md", "a.md"]);
  });

  it("promotes an already-present path instead of duplicating it", () => {
    let list = [f("a.md"), f("b.md"), f("c.md")];
    list = pushRecent(list, f("c.md"));
    expect(list.map((e) => e.path)).toEqual(["c.md", "a.md", "b.md"]);
    expect(list).toHaveLength(3);
  });

  it("refreshes the stored title when a path is re-opened", () => {
    // The title is resolved server-side and can change (an agent renamed, a
    // chat's first heading edited); the newest label wins.
    const list = pushRecent([f("chats/x.md", "old title")], f("chats/x.md", "new title"));
    expect(list[0].title).toBe("new title");
  });

  it("caps the retained history", () => {
    let list: RecentFile[] = [];
    for (let i = 0; i < 50; i++) list = pushRecent(list, f(`n${i}.md`));
    expect(list.length).toBeLessThanOrEqual(20);
    expect(list[0].path).toBe("n49.md");
  });
});

describe("readRecent", () => {
  it("returns an empty list when nothing is stored", () => {
    expect(readRecent(WS)).toEqual([]);
  });

  // This runs during render to decide what to auto-open, so throwing here
  // would blank the whole knowledge base page.
  it("survives a corrupt stored value", () => {
    localStorage.setItem(recentStorageKey(WS), "{not json");
    expect(readRecent(WS)).toEqual([]);
  });

  it("survives a stored value of the wrong shape", () => {
    localStorage.setItem(recentStorageKey(WS), JSON.stringify({ nope: true }));
    expect(readRecent(WS)).toEqual([]);
  });

  it("keeps the valid entries and drops malformed ones", () => {
    localStorage.setItem(
      recentStorageKey(WS),
      JSON.stringify([f("good.md"), { path: 42 }, null, { title: "no path" }, f("also-good.md")]),
    );
    expect(readRecent(WS).map((e) => e.path)).toEqual(["good.md", "also-good.md"]);
  });

  it("rejects an entry with an empty path, which would navigate nowhere", () => {
    localStorage.setItem(recentStorageKey(WS), JSON.stringify([{ path: "", title: "x" }]));
    expect(readRecent(WS)).toEqual([]);
  });
});

// Workspaces are fully isolated tenants and these entries are workspace-relative
// vault paths, so history must never cross between them. A shared key would show
// workspace A's notes while the user is in B — and auto-open could silently land
// on B's same-NAMED file under A's title, which looks like the right note and is
// not.
describe("workspace scoping", () => {
  it("keeps each workspace's history separate", () => {
    localStorage.setItem(recentStorageKey("ws-a"), JSON.stringify([f("notes/a-only.md")]));
    localStorage.setItem(recentStorageKey("ws-b"), JSON.stringify([f("notes/b-only.md")]));

    expect(readRecent("ws-a").map((e) => e.path)).toEqual(["notes/a-only.md"]);
    expect(readRecent("ws-b").map((e) => e.path)).toEqual(["notes/b-only.md"]);
  });

  it("does not read another workspace's list when its own is empty", () => {
    localStorage.setItem(recentStorageKey("ws-a"), JSON.stringify([f("notes/a-only.md")]));
    expect(readRecent("ws-b")).toEqual([]);
  });

  it("gives distinct keys per workspace", () => {
    expect(recentStorageKey("ws-a")).not.toBe(recentStorageKey("ws-b"));
  });
});
