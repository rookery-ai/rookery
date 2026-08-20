#!/usr/bin/env python3
"""Verify the chat progress card's layout invariants in a real browser.

jsdom has no layout engine, so the vitest suite for this can only assert that
the CSS declaration is present. This asserts the behaviour it exists to
produce, which is the only way the bug is observable.

The bug, as reported: at the start of a conversation the tool calls appeared in
a card; once the transcript filled the screen the card "doesn't scroll, it's
just sitting behind the chat text box"; and once completely full there was only
"three dots and afterwards a line and no box with the tool calls at all" — while
the assistant's reply still arrived normally.

The cause is not the stream and not React. ChatScroll is
`flex flex-1 flex-col gap-3 overflow-y-auto`, so its children are flex items.
A flex item's AUTOMATIC MINIMUM SIZE is normally content-based, which is why
message bubbles never compress — but per CSS Flexbox 4.5 an item whose overflow
is not `visible` gets an automatic minimum size of ZERO instead. ActivityCard
carries `overflow-hidden` for its rounded corners, so it was the one child flex
was allowed to collapse, and it collapsed to its 2px border: "a line".

The agent designer never showed this because it wraps the card in a plain div,
making the DIV the flex item and the card an ordinary block inside it.

Requires the `playwright` Python package with its Chromium browser installed.
Exits non-zero, describing what regressed.

    python3 scripts/verify-chat-progress-layout.py
"""
import sys

from playwright.sync_api import sync_playwright

# The classes that matter, transcribed from ChatScroll.tsx and ActivityCard.tsx.
# Kept as literal CSS rather than driving the real app so this runs with no
# server, no session and no data — the invariant is pure layout.
PAGE = """
<!doctype html><meta charset=utf-8>
<style>
  * { box-sizing: border-box; margin: 0; }
  .frame { height: 600px; display: flex; flex-direction: column; }
  /* ChatScroll: flex flex-1 flex-col gap-3 overflow-y-auto px-4 py-4 */
  .scroll { display: flex; flex: 1 1 0%; flex-direction: column; gap: 12px;
            overflow-y: auto; padding: 16px; }
  /* ChatMessageBubble: overflow visible, so its minimum size is content-based */
  .bubble { width: 100%; }
  .bubble > div { display: inline-block; background: #eee; border-radius: 12px;
                  padding: 8px 12px; }
  /* ActivityCard */
  .card { width: 100%; overflow: hidden; border-radius: 12px; border: 1px solid #ccc; }
  .card.fixed { flex-shrink: 0; }
  .card .head { display: flex; align-items: center; gap: 10px; padding: 10px 14px; }
  .card .body { padding: 8px 14px; font-family: monospace; font-size: 12px; }
  /* Composer */
  .composer { flex-shrink: 0; border-top: 1px solid #ccc; padding: 12px; }
</style>
<div class="frame">
  <div class="scroll" id="scroll"></div>
  <div class="composer" id="composer">composer</div>
</div>
<script>
  function build(nBubbles, fixed) {
    const s = document.getElementById('scroll');
    s.innerHTML = '';
    for (let i = 0; i < nBubbles; i++) {
      const b = document.createElement('div');
      b.className = 'bubble';
      b.innerHTML = '<div>message ' + i + ' - some conversational text here</div>';
      s.appendChild(b);
    }
    const c = document.createElement('div');
    c.className = 'card' + (fixed ? ' fixed' : '');
    c.id = 'card';
    c.innerHTML = '<div class="head"><b>Working</b> <span>0:12</span></div>' +
                  '<div class="body">read_file(notes.md)</div>';
    s.appendChild(c);
    // What ChatScroll's dependency-less effect does on every render.
    s.scrollTop = s.scrollHeight;
    return {
      card: c.getBoundingClientRect(),
      scroll: s.getBoundingClientRect(),
      composerTop: document.getElementById('composer').getBoundingClientRect().top,
      scrollable: s.scrollHeight > s.clientHeight,
    };
  }
</script>
"""

FULL = 40  # enough messages to overflow a 600px frame


def main() -> int:
    failures = []
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page(viewport={"width": 900, "height": 600})
        page.set_content(PAGE)

        roomy = page.evaluate(f"build(2, true)")
        full = page.evaluate(f"build({FULL}, true)")
        regressed = page.evaluate(f"build({FULL}, false)")

        # 1. The card keeps its height once the transcript fills the container.
        if full["card"]["height"] < roomy["card"]["height"] - 1:
            failures.append(
                f"card collapsed as the transcript filled: "
                f"{roomy['card']['height']:.0f}px -> {full['card']['height']:.0f}px. "
                "ActivityCard has lost its shrink-0."
            )

        # 2. Autoscroll leaves it FULLY visible, not clipped under the composer.
        card_bottom = full["card"]["y"] + full["card"]["height"]
        if card_bottom > full["scroll"]["y"] + full["scroll"]["height"] + 1:
            failures.append(
                f"card bottom ({card_bottom:.0f}px) sits past the scroll area "
                f"({full['scroll']['y'] + full['scroll']['height']:.0f}px) after "
                "scrolling to the bottom — it is hidden behind the composer."
            )
        if card_bottom > full["composerTop"] + 1:
            failures.append(
                f"card bottom ({card_bottom:.0f}px) overlaps the composer "
                f"({full['composerTop']:.0f}px)."
            )

        # 3. The transcript is genuinely scrollable, so there ARE scroll options.
        if not full["scrollable"]:
            failures.append("the transcript did not become scrollable when overfull.")

        # 4. The guard itself still detects the regression it was written for —
        #    without this, a change that made the check vacuous would pass.
        if regressed["card"]["height"] >= roomy["card"]["height"] - 1:
            failures.append(
                "the control case did NOT reproduce the collapse, so this check "
                "no longer proves anything: without shrink-0 the card measured "
                f"{regressed['card']['height']:.0f}px, expected it to collapse."
            )

        print(f"card height, room to spare:      {roomy['card']['height']:.0f}px")
        print(f"card height, transcript full:    {full['card']['height']:.0f}px")
        print(f"card height, full without fix:   {regressed['card']['height']:.0f}px")
        print(f"card fully above the composer:   {card_bottom <= full['composerTop'] + 1}")
        print(f"transcript scrollable:           {full['scrollable']}")
        browser.close()

    if failures:
        print("\nFAILED:")
        for f in failures:
            print(f"  - {f}")
        return 1
    print("\nchat progress layout: all invariants hold")
    return 0


if __name__ == "__main__":
    sys.exit(main())
