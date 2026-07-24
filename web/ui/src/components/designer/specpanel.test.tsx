import { render, screen, fireEvent } from "@testing-library/react";
import { SpecPanel, parseSchedule, parseSkills, parseConnections } from "./SpecPanel";

describe("SpecPanel", () => {
  test("empty-states before a build", () => {
    render(<SpecPanel agentMD="" tools={{}} />);
    expect(screen.getByText(/appears here once you.*built the agent/i)).toBeInTheDocument();
  });

  test("renders the brief as markdown, not raw text", () => {
    render(<SpecPanel agentMD={"# Daily digest\n\nSummarises your mail."} tools={{}} />);
    expect(screen.getByRole("heading", { name: "Daily digest" })).toBeInTheDocument();
  });

  test("lists tool files, collapsed, expandable", () => {
    render(<SpecPanel agentMD="# X" tools={{ "tools/main.py": "print('hi')" }} />);
    expect(screen.getByText("tools/main.py")).toBeInTheDocument();
    expect(screen.queryByText("print('hi')")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("tools/main.py"));
    expect(screen.getByText("print('hi')")).toBeInTheDocument();
  });

  test("collapses an expanded file back on a second click", () => {
    render(<SpecPanel agentMD="# X" tools={{ "tools/main.py": "print('hi')" }} />);
    fireEvent.click(screen.getByText("tools/main.py"));
    expect(screen.getByText("print('hi')")).toBeInTheDocument();
    fireEvent.click(screen.getByText("tools/main.py"));
    expect(screen.queryByText("print('hi')")).not.toBeInTheDocument();
  });

  test("shows the schedule in plain language", () => {
    render(<SpecPanel agentMD={"# Suggested schedule: */10 * * * *\n# X"} tools={{}} />);
    expect(screen.getByText(/every 10 minutes/i)).toBeInTheDocument();
  });

  test("lists declared skills and connections", () => {
    render(
      <SpecPanel
        agentMD={"# Skills: pdf, web-search\n# Connections: gmail\n# X"}
        tools={{}}
      />,
    );
    expect(screen.getByText(/pdf/)).toBeInTheDocument();
    expect(screen.getByText(/gmail/)).toBeInTheDocument();
  });

  test("does not render a schedule/skills/connections row when none are declared", () => {
    render(<SpecPanel agentMD={"# Just a brief"} tools={{}} />);
    expect(screen.queryByText(/every|schedule:/i)).not.toBeInTheDocument();
  });

  test("multiple tool files each toggle independently", () => {
    render(
      <SpecPanel
        agentMD="# X"
        tools={{ "tools/a.py": "print('a')", "tools/b.py": "print('b')" }}
      />,
    );
    fireEvent.click(screen.getByText("tools/a.py"));
    expect(screen.getByText("print('a')")).toBeInTheDocument();
    expect(screen.queryByText("print('b')")).not.toBeInTheDocument();
  });
});

describe("parseSchedule", () => {
  test("every-N-minutes shape", () => {
    expect(parseSchedule("# Suggested schedule: */10 * * * *\nbody")).toMatch(/every 10 minutes/i);
  });

  test("hourly shape", () => {
    expect(parseSchedule("# Suggested schedule: 0 * * * *\nbody")).toMatch(/every hour/i);
  });

  test("daily-at-hour shape", () => {
    expect(parseSchedule("# Suggested schedule: 0 9 * * *\nbody")).toMatch(/every day at 09:00/i);
  });

  test("weekly-on-weekday shape", () => {
    expect(parseSchedule("# Suggested schedule: 0 9 * * 1\nbody")).toMatch(/every monday at 09:00/i);
  });

  test("unrecognised cron shape falls back to the raw expression", () => {
    const got = parseSchedule("# Suggested schedule: 15,45 * * * *\nbody");
    expect(got).toBe("schedule: 15,45 * * * *");
  });

  test("none schedule returns null", () => {
    expect(parseSchedule("# Suggested schedule: none\nbody")).toBeNull();
  });

  test("no header at all returns null", () => {
    expect(parseSchedule("Just a body, no header.")).toBeNull();
  });

  test("every-1-minute shape uses singular grammar, not 'every 1 minutes'", () => {
    expect(parseSchedule("# Suggested schedule: */1 * * * *\nbody")).toBe("every minute");
  });

  // Bound-checking regressions (reviewer-verified failures): the shape
  // regexes match digits without range-checking them, so an out-of-range
  // step/hour/weekday must fall through to the raw-cron fallback instead of
  // emitting confidently wrong prose.
  test("step of 0 is not an interval — falls back to raw cron, not 'every 0 minutes'", () => {
    expect(parseSchedule("# Suggested schedule: */0 * * * *\nbody")).toBe(
      "schedule: */0 * * * *",
    );
  });

  test("step of 90 in a 0-59 minute field actually runs hourly — falls back, not 'every 90 minutes'", () => {
    expect(parseSchedule("# Suggested schedule: */90 * * * *\nbody")).toBe(
      "schedule: */90 * * * *",
    );
  });

  test("step of 61 in a 0-59 minute field actually runs hourly — falls back, not 'every 61 minutes'", () => {
    expect(parseSchedule("# Suggested schedule: */61 * * * *\nbody")).toBe(
      "schedule: */61 * * * *",
    );
  });

  test("hour 25 doesn't exist — falls back, not 'every day at 25:00'", () => {
    expect(parseSchedule("# Suggested schedule: 0 25 * * *\nbody")).toBe(
      "schedule: 0 25 * * *",
    );
  });

  test("hour 99 doesn't exist — falls back, not 'every day at 99:00'", () => {
    expect(parseSchedule("# Suggested schedule: 0 99 * * *\nbody")).toBe(
      "schedule: 0 99 * * *",
    );
  });

  test("hour 24 doesn't exist — falls back, not 'every day at 24:00'", () => {
    expect(parseSchedule("# Suggested schedule: 0 24 * * *\nbody")).toBe(
      "schedule: 0 24 * * *",
    );
  });

  test("weekday 7 (cron's alternate Sunday) is in-range and resolves to Sunday", () => {
    expect(parseSchedule("# Suggested schedule: 0 9 * * 7\nbody")).toMatch(/every sunday at 09:00/i);
  });

  test("weekday 8 is out of range — falls back to raw cron", () => {
    expect(parseSchedule("# Suggested schedule: 0 9 * * 8\nbody")).toBe(
      "schedule: 0 9 * * 8",
    );
  });
});

describe("parseSkills", () => {
  test("comma-separated list", () => {
    expect(parseSkills("# Skills: pdf, web-search\nbody")).toEqual(["pdf", "web-search"]);
  });

  test("none returns an empty list", () => {
    expect(parseSkills("# Skills: none\nbody")).toEqual([]);
  });

  test("no header returns an empty list", () => {
    expect(parseSkills("no header here")).toEqual([]);
  });
});

describe("parseConnections", () => {
  test("single connection", () => {
    expect(parseConnections("# Connections: gmail\nbody")).toEqual(["gmail"]);
  });

  test("none returns an empty list", () => {
    expect(parseConnections("# Connections: none\nbody")).toEqual([]);
  });

  test("no header returns an empty list", () => {
    expect(parseConnections("no header here")).toEqual([]);
  });
});
