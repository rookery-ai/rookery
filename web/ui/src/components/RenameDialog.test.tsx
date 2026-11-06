import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ToastHost, ToastProvider } from "@/components/shell/Toast";
import { ApiError } from "@/lib/api";
import { renameNotice, type RenameResult } from "@/lib/rename";
import { SkillView } from "@/pages/skills/SkillView";
import { RenameDialog } from "./RenameDialog";

const ok = (over: Partial<RenameResult> = {}): RenameResult => ({
  id: "a1",
  name: "Price tracker",
  still_mentions: [],
  warnings: [],
  env_removed: false,
  ...over,
});

function setup(onRename: (name: string) => Promise<RenameResult>) {
  const onClose = vi.fn();
  render(
    <ToastProvider>
      <ToastHost />
      <RenameDialog label="agent" currentName="Price watch" onClose={onClose} onRename={onRename} />
    </ToastProvider>,
  );
  return { onClose, field: screen.getByLabelText("New name"), submit: screen.getByRole("button", { name: "Rename" }) };
}

test("starts from the current name and cannot submit it unchanged or empty", async () => {
  const user = userEvent.setup();
  const { field, submit } = setup(async () => ok());
  expect(field).toHaveValue("Price watch");
  expect(submit).toBeDisabled();
  await user.clear(field);
  expect(submit).toBeDisabled();
  await user.type(field, "   ");
  expect(submit).toBeDisabled();
});

test("a successful rename sends the trimmed name and closes", async () => {
  const user = userEvent.setup();
  const onRename = vi.fn(async () => ok());
  const { onClose, field, submit } = setup(onRename);
  await user.clear(field);
  await user.type(field, "  Price tracker ");
  await user.click(submit);
  expect(onRename).toHaveBeenCalledWith("Price tracker");
  expect(onClose).toHaveBeenCalled();
});

test("a refusal stays in the open dialog, with the field usable", async () => {
  const user = userEvent.setup();
  const msg = "It is running. Wait for it to finish, then rename.";
  const { onClose, field, submit } = setup(async () => {
    throw new ApiError(409, "agent_running", msg);
  });
  await user.clear(field);
  await user.type(field, "Price tracker");
  await user.click(submit);
  expect(await screen.findByRole("alert")).toHaveTextContent(msg);
  expect(onClose).not.toHaveBeenCalled();
  expect(submit).toBeEnabled();
});

test("a rename with something to report shows it in a toast", async () => {
  const user = userEvent.setup();
  const { field, submit } = setup(async () => ok({ still_mentions: ["notes/plan.md"] }));
  await user.clear(field);
  await user.type(field, "Price tracker");
  await user.click(submit);
  expect((await screen.findAllByText(/notes\/plan\.md/)).length).toBeGreaterThan(0);
});

test("renameNotice says nothing when there is nothing to say", () => {
  expect(renameNotice(ok())).toBeNull();
  expect(renameNotice(ok({ env_removed: true }))).toMatch(/reinstall/);
  expect(renameNotice(ok({ still_mentions: ["a.md", "b.md"] }))).toMatch(/2 files still mention.*a\.md and others/);
  expect(renameNotice(ok({ warnings: ["could not update x"] }))).toMatch(/could not update x/);
});

const viewProps = { name: "report-helper", category: "Other", content: "# Body" };

test("SkillView's Rename is disabled while there are unsaved edits", async () => {
  const user = userEvent.setup();
  render(<SkillView kind="user" {...viewProps} onSave={async () => {}} onRename={() => {}} />);
  expect(screen.getByRole("button", { name: "Rename" })).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "raw" }));
  await user.type(screen.getByRole("textbox"), "x");
  expect(screen.getByRole("button", { name: "Rename" })).toBeDisabled();
});

test("a core skill offers no Rename", () => {
  render(<SkillView kind="core" {...viewProps} />);
  expect(screen.queryByRole("button", { name: "Rename" })).not.toBeInTheDocument();
});
