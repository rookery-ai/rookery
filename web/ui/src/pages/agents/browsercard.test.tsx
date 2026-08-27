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
  // The condition the whole card hangs on. An earlier version put a permissions
  // card on every agent, including ones that only read a page — which teaches an
  // owner to tick whatever is in front of them, and makes the warning worthless
  // on the agent that actually needed it.
  it("shows nothing for an agent that does nothing irreversible", () => {
    const { container } = render(
      <BrowserCard
        agentId="a1"
        grants={{ available: true, irreversible: false, needs_irreversible: false }}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("shows nothing when the server has no browser", () => {
    const { container } = render(
      <BrowserCard
        agentId="a1"
        grants={{ available: false, irreversible: false, needs_irreversible: true }}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("asks, and explains what happens meanwhile, when the agent needs it", () => {
    render(
      <BrowserCard
        agentId="a1"
        grants={{ available: true, irreversible: false, needs_irreversible: true }}
      />,
    );
    expect(screen.getByRole("checkbox")).not.toBeChecked();
    // The guidance half: without it the owner meets a stopped agent and a
    // refusal buried in a run log, and has to deduce that a switch exists.
    expect(screen.getByText(/stop, and tell you what it would have done/i)).toBeInTheDocument();
  });

  it("offers exactly one permission", () => {
    render(
      <BrowserCard
        agentId="a1"
        grants={{ available: true, irreversible: false, needs_irreversible: true }}
      />,
    );
    // There is no separate switch for clicking or signing in — that one gated
    // nothing, since an agent can do the same with bash and curl.
    expect(screen.getAllByRole("checkbox")).toHaveLength(1);
  });

  it("warns in plain language once the agent can spend money unattended", () => {
    render(
      <BrowserCard
        agentId="a1"
        grants={{ available: true, irreversible: true, needs_irreversible: true }}
      />,
    );
    expect(screen.getByText(/spend money without asking/i)).toBeInTheDocument();
  });

  it("does not warn about spending before the permission is given", () => {
    render(
      <BrowserCard
        agentId="a1"
        grants={{ available: true, irreversible: false, needs_irreversible: true }}
      />,
    );
    expect(screen.queryByText(/spend money without asking/i)).not.toBeInTheDocument();
  });
});
