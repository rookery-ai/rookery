import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router";
import { AppShell, useSlideOver } from "@/components/shell/AppShell";
import { ChatAppWizard } from "./ChatAppWizard";
import type { ConnectorPlatform } from "@/lib/connections";
import { useEffect } from "react";

// Slack slash commands CANNOT be registered with a bot token — they are declared
// in the app manifest. The manifest block is therefore not a convenience: it is
// the only route to a Slack command menu at all, and it has to appear on the
// setup step whose instructions tell the user to paste it.

const SLACK: ConnectorPlatform = {
  platform: "slack",
  label: "Slack",
  blurb: "Slack is where your assistant will message you.",
  setup_steps: ["Create New App → From an app manifest"],
  fields: [{ name: "token", label: "Bot Token", secret: true }],
  connected: false,
  identity: "",
  linked: false,
  linked_identity: "",
  primary: false,
  dm_url: "",
  invite_url: "",
  bot_online: false,
};

const MANIFEST =
  "display_information:\n  name: Rookery\nfeatures:\n  slash_commands:\n    - command: /agent";

function Opener({ platform }: { platform: ConnectorPlatform }) {
  const { open } = useSlideOver();
  useEffect(() => {
    open(<ChatAppWizard platform={platform} />, { title: platform.label });
  }, [open, platform]);
  return null;
}

function wrap(platform: ConnectorPlatform) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<Opener platform={platform} />} />
          </Route>
        </Routes>
      </MemoryRouter>,
    </QueryClientProvider>,
  );
}

test("a platform carrying a setup manifest renders it as copyable code", async () => {
  wrap({ ...SLACK, setup_manifest: MANIFEST });

  expect(await screen.findByText("App manifest")).toBeInTheDocument();
  expect(screen.getByText(/slash_commands/)).toBeInTheDocument();
  // Copyable, because the entire purpose is pasting it into Slack.
  expect(screen.getByRole("button", { name: /copy/i })).toBeInTheDocument();
});

test("a platform with no manifest renders no manifest block", async () => {
  wrap(SLACK);

  await screen.findByText(/From an app manifest/);
  expect(screen.queryByText("App manifest")).not.toBeInTheDocument();
});
