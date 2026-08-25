import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { BrowserCard } from "./BrowserCard";

const saveBrowserGrants = vi.fn();
vi.mock("@/lib/agents", async (orig) => {
  const actual = await orig<typeof import("@/lib/agents")>();
  return {
    ...actual,
    useAgentActions: () => ({ saveBrowserGrants }),
  };
});

describe("BrowserCard", () => {
  it("tells the operator how to install a missing browser instead of greying a switch", () => {
    render(
      <BrowserCard
        agentId="a1"
        grants={{ available: false, acting: false, irreversible: false }}
      />,
    );
    // Naming the command is the difference between a dead control and a fix.
    expect(screen.getByText(/rookery browser install/)).toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("offers acting and irreversible as two separate decisions", () => {
    render(
      <BrowserCard
        agentId="a1"
        grants={{ available: true, acting: false, irreversible: false }}
      />,
    );
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes).toHaveLength(2);
    expect(boxes[0]).not.toBeChecked();
    expect(boxes[1]).not.toBeChecked();
  });

  // The irreversible switch must be unreachable until acting is granted:
  // "may pay" without "may click" is a state the server cannot enforce, so
  // offering it would show a permission that silently does nothing.
  it("locks the irreversible switch until acting is granted", () => {
    render(
      <BrowserCard
        agentId="a1"
        grants={{ available: true, acting: false, irreversible: false }}
      />,
    );
    expect(screen.getAllByRole("checkbox")[1]).toBeDisabled();
  });

  it("unlocks the irreversible switch once acting is granted", () => {
    render(
      <BrowserCard
        agentId="a1"
        grants={{ available: true, acting: true, irreversible: false }}
      />,
    );
    expect(screen.getAllByRole("checkbox")[1]).toBeEnabled();
  });

  // The one screen in the product where a mis-set switch spends real money
  // must say so in words, not just leave a checkbox ticked.
  it("warns in plain language when the agent can spend money unattended", () => {
    render(
      <BrowserCard
        agentId="a1"
        grants={{ available: true, acting: true, irreversible: true }}
      />,
    );
    expect(screen.getByText(/spend money without asking/i)).toBeInTheDocument();
  });

  it("does not warn about spending when the grant is not given", () => {
    render(
      <BrowserCard
        agentId="a1"
        grants={{ available: true, acting: true, irreversible: false }}
      />,
    );
    expect(screen.queryByText(/spend money without asking/i)).not.toBeInTheDocument();
  });
});
