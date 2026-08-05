import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LinkStep } from "./LinkStep";
import { ESCALATE_MS, type PlatformSource } from "./source";
import type { ConnectorPlatform } from "@/lib/connections";

const BASE: ConnectorPlatform = {
  platform: "discord",
  label: "Discord",
  blurb: "",
  setup_steps: [],
  fields: [],
  connected: true,
  identity: "rookery",
  linked: false,
  linked_identity: "",
  primary: false,
  dm_url: "https://discord.com/users/1",
  invite_url: "https://discord.com/invite",
  bot_online: true,
};

// A source that always reports the given platform. The shared step must not
// reach for a transport of its own — that coupling is what kept it out of the
// setup wizard, where every connector route 403s.
function sourceFor(p: ConnectorPlatform): PlatformSource {
  return {
    usePlatform: () => p,
    useTest: () => ({ mutate: () => {}, isPending: false }) as never,
  };
}

function renderStep(p: ConnectorPlatform) {
  return render(
    <LinkStep
      platform={p}
      source={sourceFor(p)}
      onFinishLater={() => {}}
      onDone={() => {}}
    />,
  );
}

describe("LinkStep", () => {
  it("renders no Done button and no linked confirmation while unlinked", () => {
    renderStep(BASE);
    expect(screen.queryByRole("button", { name: /^Done$/ })).toBeNull();
    expect(screen.queryByText(/Linked as/)).toBeNull();
  });

  it("waits on /start when the bot is online", () => {
    renderStep(BASE);
    expect(screen.getByText(/Waiting for you to send/i)).toBeTruthy();
    expect(screen.queryByText(/isn't running/i)).toBeNull();
  });

  it("states the bot is not running when it is offline", () => {
    renderStep({ ...BASE, bot_online: false });
    // The whole point: a dead server must not render as "waiting for you".
    expect(screen.getByText(/isn't running/i)).toBeTruthy();
  });

  it("confirms the link once the identity row lands", () => {
    renderStep({ ...BASE, linked: true, linked_identity: "tickbrick" });
    expect(screen.getByText(/Linked as tickbrick/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /^Done$/ })).toBeTruthy();
  });

  it("offers the invite and DM links while unlinked", () => {
    renderStep(BASE);
    expect(
      screen.getByRole("link", { name: /Invite to a server/i }),
    ).toHaveAttribute("href", "https://discord.com/invite");
    expect(screen.getByRole("link", { name: /Open Discord/i })).toHaveAttribute(
      "href",
      "https://discord.com/users/1",
    );
  });
});

describe("LinkStep escalation", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("surfaces troubleshooting help only after the escalation threshold", () => {
    renderStep(BASE);
    expect(screen.queryByText(/Not working/i)).toBeNull();

    act(() => {
      vi.advanceTimersByTime(ESCALATE_MS + 1000);
    });

    expect(screen.getByText(/Not working/i)).toBeTruthy();
    // The single most common Discord mistake, and the one the product was
    // previously silent about: /start typed in a server channel is discarded
    // by mapDiscordDM with no reply at all.
    expect(
      screen.getByText(/message posted in a server channel is ignored/i),
    ).toBeTruthy();
  });
});
