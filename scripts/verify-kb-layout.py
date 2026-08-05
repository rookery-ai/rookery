#!/usr/bin/env python3
"""Verify the two KB layout fixes against a running instance.

jsdom has no layout engine and no scrolling, so the vitest suites for these
fixes can only assert that the CSS declarations are present. This asserts the
behaviour they are supposed to produce, which is the only way either bug is
observable:

  1. Scrolling past the end of a long note must not move the app shell. Before
     the fix, wheeling once more with the editor pane already at its bottom
     chained out to the document: documentElement.scrollTop went 0 -> 1459 and
     the icon rail's top went 0 -> -1459, scrolling the whole h-screen frame
     out of the viewport.

  2. The slash menu must stay inside the viewport. Before the fix, placement
     was `top = caret.bottom + 4` with no bounds check, so with the caret on
     the last line of a long note the 442px popup rendered at top 868 in a
     900px viewport — 410px below the fold.

Point it at a THROWAWAY instance, never at real data:

    python3 scripts/verify-kb-layout.py <sa_session-cookie> [base-url]

Requires the `playwright` Python package with its Chromium browser installed.
Exits non-zero, describing what regressed.
"""
import sys

from playwright.sync_api import sync_playwright

COOKIE = sys.argv[1] if len(sys.argv) > 1 else ""
BASE = sys.argv[2] if len(sys.argv) > 2 else "http://127.0.0.1:8099"
NOTE = "notes%2Fverify-long.md"

if not COOKIE:
    sys.exit(__doc__)

failures = []


def check(name, ok, detail):
    print(("PASS  " if ok else "FAIL  ") + name + "\n      " + detail)
    if not ok:
        failures.append(name)


with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page(viewport={"width": 1600, "height": 900})
    page.context.add_cookies([{
        "name": "sa_session", "value": COOKIE,
        "domain": "127.0.0.1", "path": "/", "httpOnly": True,
    }])

    page.goto(BASE + "/kb?path=" + NOTE)
    page.wait_for_selector(".note-editor-content .tiptap", timeout=20000)
    page.wait_for_timeout(900)

    # 1. Overscroll must not chain out of the editor pane.
    page.evaluate("""() => {
        const pane = document.querySelector('.note-editor-content .tiptap')
                       .closest('.overflow-y-auto');
        pane.scrollTop = pane.scrollHeight;
    }""")
    page.mouse.move(900, 500)
    for _ in range(6):
        page.mouse.wheel(0, 400)
    page.wait_for_timeout(400)
    st = page.evaluate("""() => ({
        doc: document.documentElement.scrollTop,
        rail: Math.round(document.querySelector('aside').getBoundingClientRect().top),
    })""")
    check(
        "app shell stays put when scrolling past the end of a note",
        st["doc"] == 0 and st["rail"] == 0,
        "documentElement.scrollTop=%s rail.top=%s (both must be 0)"
        % (st["doc"], st["rail"]),
    )

    # 2. The slash menu must stay inside the viewport with the caret at the
    #    very bottom — the position the original placement could not survive.
    page.goto(BASE + "/kb?path=" + NOTE)
    page.wait_for_selector(".note-editor-content .tiptap")
    page.wait_for_timeout(900)
    page.evaluate("""() => {
        const t = document.querySelector('.note-editor-content .tiptap');
        t.closest('.overflow-y-auto').scrollTop = 1e6;
        const last = t.lastElementChild;
        const r = document.createRange();
        r.selectNodeContents(last); r.collapse(false);
        const s = getSelection(); s.removeAllRanges(); s.addRange(r);
        t.focus();
    }""")
    page.keyboard.type(" /")
    page.wait_for_timeout(700)
    menu = page.evaluate("""() => {
        const n = [...document.body.children].filter(
            e => e.style && e.style.position === 'fixed' && e.style.zIndex === '50');
        if (!n.length) return null;
        const r = n[n.length - 1].getBoundingClientRect();
        return {top: Math.round(r.top), bottom: Math.round(r.bottom),
                left: Math.round(r.left), right: Math.round(r.right),
                h: Math.round(r.height), vh: innerHeight, vw: innerWidth};
    }""")
    if menu is None:
        check("slash menu opens", False, "no popup element found")
    else:
        check(
            "slash menu fits inside the viewport",
            menu["top"] >= 0 and menu["bottom"] <= menu["vh"]
            and menu["left"] >= 0 and menu["right"] <= menu["vw"],
            "top=%(top)s bottom=%(bottom)s height=%(h)s (viewport %(vh)s) | "
            "left=%(left)s right=%(right)s (viewport %(vw)s)" % menu,
        )

    browser.close()

if failures:
    print("\n%d check(s) failed: %s" % (len(failures), ", ".join(failures)))
    sys.exit(1)
print("\nall checks passed")
