import { describe, expect, it } from "vitest";
import { activityTitle } from "./activityTitle";

describe("activityTitle", () => {
  it("stops saying 'Building' once the build is done", () => {
    expect(activityTitle("build", "live", "agent")).toBe("Building your agent…");
    expect(activityTitle("build", "done", "agent")).toBe("Built and tested your agent");
    expect(activityTitle("build", "done", "skill")).toBe("Built and tested your skill");
    expect(activityTitle("build", "error", "agent")).toBe("Build stopped");
  });
  it("never says 'Built and tested' for a build that failed", () => {
    // A failed build's stream also ends with the server's `done` event.
    expect(activityTitle("build", "done", "agent", true)).toBe("Build stopped");
    expect(activityTitle("build", "live", "agent", true)).toBe("Building your agent…");
  });
  it("keeps the turn card's title regardless of status", () => {
    expect(activityTitle("turn", "live", "agent")).toBe("Looking through your knowledge base…");
    expect(activityTitle("turn", "done", "agent")).toBe("Looking through your knowledge base…");
  });
});
