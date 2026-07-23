import { assetDisplayURL, vaultPathFromSrc } from "./kbImage";
import { fidelityRoundTrip } from "./editor";

test("assetDisplayURL maps a vault path to the served URL, leaves external URLs alone", () => {
  expect(assetDisplayURL("assets/pic.png")).toBe("/api/v1/kb/raw?path=assets%2Fpic.png");
  expect(assetDisplayURL("https://x.com/i.png")).toBe("https://x.com/i.png");
  expect(assetDisplayURL("data:image/png;base64,AAAA")).toBe("data:image/png;base64,AAAA");
});

test("vaultPathFromSrc reverses a served URL back to the portable path", () => {
  expect(vaultPathFromSrc("/api/v1/kb/raw?path=assets%2Fpic.png")).toBe("assets/pic.png");
  expect(vaultPathFromSrc("assets/pic.png")).toBe("assets/pic.png");
  expect(vaultPathFromSrc("https://x.com/i.png")).toBe("https://x.com/i.png");
});

test("an image note keeps its portable ![](assets/…) markdown across a round-trip", () => {
  const md = "Here is a picture:\n\n![diagram](assets/pic.png)";
  // The stored markdown must not become the served URL — it stays portable.
  expect(fidelityRoundTrip(md)).toContain("![diagram](assets/pic.png)");
  expect(fidelityRoundTrip(md)).not.toContain("/api/v1/kb/raw");
});
