import { render, screen, fireEvent } from "@testing-library/react";
import { SpecPanel, parseSchedule, parseSkills, parseConnections } from "./SpecPanel";

describe("SpecPanel", () => {
  test("empty-states before a build", () => {
    render(<SpecPanel agentMD="" tools={{}} />);
    expect(screen.getByText(/nothing built yet/i)).toBeInTheDocument();
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
