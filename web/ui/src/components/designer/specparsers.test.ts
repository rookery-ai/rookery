import { parseMCP, parseTechnicalSpec } from "./SpecPanel";

// The panel listed skills and connections and silently omitted MCP servers, so
// an agent bound to one showed no sign of it. The heading has two spellings in
// internal/agentdesigner/parse_mcp.go and both must be read here.
test("parseMCP reads both heading spellings", () => {
  expect(parseMCP("# MCP: weather, files\n\nBody")).toEqual(["weather", "files"]);
  expect(parseMCP("# MCP servers: weather\n\nBody")).toEqual(["weather"]);
  expect(parseMCP("# MCP: none\n")).toEqual([]);
  expect(parseMCP("no header at all")).toEqual([]);
});

test("parseTechnicalSpec reads the labels the designer is asked to emit", () => {
  const fields = parseTechnicalSpec(
    [
      "Tier: 1",
      "Schedule: 0 8 * * *",
      "Notifies user: yes ([CHAT] contains: the deal list)",
      "Knowledge base writes: notes/deals.md",
      "Secrets: none",
      "External services: none",
    ].join("\n"),
  );
  expect(fields.map((f) => f.label)).toEqual([
    "Tier",
    "Schedule",
    "Notifies user",
    "Knowledge base writes",
    "Secrets",
    "External services",
  ]);
  // A value containing its own colon must survive intact — "Notifies user" is
  // the field most likely to carry one.
  expect(fields.find((f) => f.label === "Notifies user")?.value).toBe(
    "yes ([CHAT] contains: the deal list)",
  );
});

test("parseTechnicalSpec reads the edit-mode labels too", () => {
  const fields = parseTechnicalSpec("Change: fetch the page directly\nRoot cause: the script 404ed\nTier change: 2→1");
  expect(fields.map((f) => f.label)).toEqual(["Change", "Root cause", "Tier change"]);
});

// Same policy as parseSchedule: render only what can be proved. Returning []
// is the caller's signal to show the raw block, which is strictly better than a
// confidently wrong summary of what an agent is about to do.
test("parseTechnicalSpec ignores unrecognised labels and empty values", () => {
  expect(parseTechnicalSpec("Wingspan: 2m\nVibe: good")).toEqual([]);
  expect(parseTechnicalSpec("Tier:")).toEqual([]);
  expect(parseTechnicalSpec("")).toEqual([]);
  expect(parseTechnicalSpec("just some prose with no colon")).toEqual([]);
});

test("parseTechnicalSpec is case-insensitive on the label but normalises it", () => {
  expect(parseTechnicalSpec("tier: 3")).toEqual([{ label: "Tier", value: "3" }]);
});
