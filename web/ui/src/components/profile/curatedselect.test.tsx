import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CuratedSelect } from "./CuratedSelect";
import {
  timezoneOptions, countryOptions, LANGUAGE_OPTIONS, TONE_OPTIONS,
} from "./options";

const OPTS = [
  { value: "direct", label: "Direct" },
  { value: "friendly", label: "Friendly" },
];

test("renders the curated options plus an unset placeholder", () => {
  render(<CuratedSelect id="t" value="" onChange={() => {}} options={OPTS} />);
  const select = screen.getByRole("combobox") as HTMLSelectElement;
  expect(Array.from(select.options).map((o) => o.value)).toEqual(["", "direct", "friendly"]);
  expect(select.value).toBe("");
});

test("a stored value that is NOT in the list is preserved and stays selected", async () => {
  // These fields were free text before, so a live profile can hold anything.
  // Dropping such a value would blank the field on load and then save "" back
  // on the next submit — silently destroying a preference the user never touched.
  render(
    <CuratedSelect id="t" value="direct but never chatty" onChange={() => {}} options={OPTS} />,
  );
  const select = screen.getByRole("combobox") as HTMLSelectElement;
  expect(select.value).toBe("direct but never chatty");
  expect(screen.getByRole("option", { name: /direct but never chatty \(current\)/ })).toBeTruthy();
});

test("a stored value already in the list is not duplicated", () => {
  render(<CuratedSelect id="t" value="direct" onChange={() => {}} options={OPTS} />);
  const select = screen.getByRole("combobox") as HTMLSelectElement;
  expect(Array.from(select.options).filter((o) => o.value === "direct")).toHaveLength(1);
  expect(select.value).toBe("direct");
});

test("selecting an option reports the value, not the label", async () => {
  const onChange = vi.fn();
  render(<CuratedSelect id="t" value="" onChange={onChange} options={OPTS} />);
  await userEvent.selectOptions(screen.getByRole("combobox"), "friendly");
  expect(onChange).toHaveBeenCalledWith("friendly");
});

test("timezone options are real IANA names", () => {
  // The value has to survive Go's time.LoadLocation — it drives when reminders
  // fire — so the label may be prettified but the value must not be.
  const opts = timezoneOptions();
  expect(opts.length).toBeGreaterThan(10);
  const utc = opts.find((o) => o.value === "UTC");
  expect(utc).toBeDefined();
  for (const o of opts.slice(0, 50)) {
    expect(o.value).not.toContain(" ");
  }
});

test("country options store a readable name rather than a bare code", () => {
  const opts = countryOptions();
  expect(opts.length).toBeGreaterThan(20);
  // Values feed an LLM prompt as context; "MK" reads as noise there.
  for (const o of opts.slice(0, 10)) {
    expect(o.value.length).toBeGreaterThan(2);
    expect(o.value).toBe(o.label);
  }
});

test("language and tone lists are non-empty and self-consistent", () => {
  expect(LANGUAGE_OPTIONS.length).toBeGreaterThan(10);
  for (const o of LANGUAGE_OPTIONS) expect(o.value).toBe(o.label);
  expect(TONE_OPTIONS.length).toBeGreaterThan(4);
  // Tone values are single lowercase words — the label carries the explanation.
  for (const o of TONE_OPTIONS) expect(o.value).toMatch(/^[a-z]+$/);
});
