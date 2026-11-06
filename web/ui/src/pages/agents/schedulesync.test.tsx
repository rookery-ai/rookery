import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ScheduleCard } from "./ScheduleCard";
import { AgentMDCard } from "./AgentMDCard";

const saveAgentMD = vi.fn().mockResolvedValue(undefined);

vi.mock("@/lib/agents", async (original) => ({
  ...(await original<typeof import("@/lib/agents")>()),
  useAgentActions: () => ({
    saveSchedule: vi.fn(),
    deleteSchedule: vi.fn(),
    saveAgentMD,
  }),
}));

const schedule = { cron_expr: "0 9 * * *", next_run_at: null, last_run_at: null, enabled: true };
const base = { changes: [], review: [], refused: 0 };

describe("ScheduleCard sync note", () => {
  it("shows nothing when no pass ran or a pass found nothing", () => {
    const { rerender, container } = render(<ScheduleCard agentId="a" schedule={schedule} sync={null} />);
    const before = container.textContent;
    rerender(<ScheduleCard agentId="a" schedule={schedule} sync={{ ...base, state: "done" }} />);
    expect(container.textContent).toBe(before);
  });

  it("says the instructions are being checked while running", () => {
    render(<ScheduleCard agentId="a" schedule={schedule} sync={{ ...base, state: "running" }} />);
    expect(screen.getByRole("status")).toHaveTextContent(/old timing/);
  });

  it("shows each change with its original text, and what was left for review", () => {
    render(
      <ScheduleCard
        agentId="a"
        schedule={schedule}
        sync={{
          ...base,
          state: "done",
          changes: [{ find: "Every day at 9am", replace: "Every Monday at 8am" }],
          review: ["Compare with yesterday's run."],
          refused: 2,
        }}
      />,
    );
    expect(screen.getByText("Every day at 9am")).toBeInTheDocument();
    expect(screen.getByText(/Every Monday at 8am/)).toBeInTheDocument();
    expect(screen.getByText("Compare with yesterday's run.")).toBeInTheDocument();
    expect(screen.getByText(/2 suggested changes/)).toBeInTheDocument();
  });

  it("says the schedule itself is saved when the pass failed", () => {
    render(
      <ScheduleCard
        agentId="a"
        schedule={schedule}
        sync={{ ...base, state: "failed", error: "The coder is out of quota." }}
      />,
    );
    expect(screen.getByText(/schedule is saved/)).toHaveTextContent("The coder is out of quota.");
  });
});

describe("AgentMDCard while the schedule changes", () => {
  it("adopts the new schedule line and keeps the owner's other edits", async () => {
    const user = userEvent.setup();
    const initial = "# Suggested schedule: 0 9 * * *\nBody.";
    const { rerender } = render(<AgentMDCard agentId="a" content={initial} missingSecrets={[]} />);
    await user.click(screen.getByRole("button", { name: /edit/i }));
    const box = screen.getByRole("textbox");
    await user.type(box, " More.");

    rerender(
      <AgentMDCard agentId="a" content={"# Suggested schedule: 5 8 * * 1\nBody."} missingSecrets={[]} />,
    );
    expect(box).toHaveValue("# Suggested schedule: 5 8 * * 1\nBody. More.");
  });
});
