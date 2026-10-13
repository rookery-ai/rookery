import { describe, expect, it } from "vitest";
import { checkFidelity } from "../editor";
import { fenceFor } from "./codeBlock";

describe("code block fences", () => {
  it("sizes the fence past the longest backtick run", () => {
    expect(fenceFor("ls -la")).toBe("```");
    expect(fenceFor("echo ```oops```")).toBe("````");
    expect(fenceFor("a ```` b")).toBe("`````");
  });
  it("a block containing ``` round-trips through the editor", () => {
    const md = "# Run\n\n````text\n🔧 bash(cd . && echo ```oops``` && ls)\n````\n";
    expect(checkFidelity(md)).toBe(true);
  });
  it("an ordinary block still uses three backticks", () => {
    expect(checkFidelity("```text\nplain\n```\n")).toBe(true);
  });
});
