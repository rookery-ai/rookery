import { fidelityRoundTrip, checkFidelity } from "./editor";

// With html:true, inline HTML an agent or a document conversion leaves in a
// note renders as its nearest markdown/text instead of showing up as escaped
// "&lt;br&gt;" garbage in the rich-text editor.
test("inline HTML is normalized to text/markdown, not escaped entities", () => {
  const out = fidelityRoundTrip("H<sub>2</sub>O and a line<br>break");
  expect(out).not.toContain("&lt;");
  expect(out).toContain("H2O"); // sub tag dropped, content kept
});

// The fidelity contract is intact: content with NO html tags is unaffected by
// html:true — plain prose, wikilinks, fenced code, and tables still round-trip
// and therefore still open in the rich-text editor.
test("non-HTML content still round-trips cleanly (opens in rich text)", () => {
  expect(checkFidelity("A paragraph with **bold**, *italics*, and a [[Wikilink]].")).toBe(true);
  expect(checkFidelity("```yaml\nname: test\nitems:\n  - a\n  - b\n```")).toBe(true);
  expect(checkFidelity("| a | b |\n| --- | --- |\n| 1 | 2 |")).toBe(true);
});

// A literal comparison in prose stays render-equivalent (escaped) exactly as
// before — html:true does not turn "a < b" into markup.
test("a literal < in prose is unchanged by html:true", () => {
  expect(fidelityRoundTrip("if a < b then stop")).toBe("if a &lt; b then stop");
});
