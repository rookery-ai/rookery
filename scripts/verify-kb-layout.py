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

    # 3. The root element must have nothing to scroll. jsdom cannot see this at
    #    all: every container from the editor pane up to <body> measured 900/900
    #    and clipped correctly, while <html> reported scrollHeight 13425 against
    #    clientHeight 900. Wheeling with the pointer over the icon rail then
    #    scrolled the document and carried the rail out of view.
    root = page.evaluate(
        "() => ({ scrollH: document.documentElement.scrollHeight,"
        "         clientH: document.documentElement.clientHeight })"
    )
    check(
        "root element has no scrollable overflow",
        root["scrollH"] <= root["clientH"] + 1,
        f"documentElement scrollHeight={root['scrollH']} clientHeight={root['clientH']}",
    )

    rail = page.locator('nav[aria-label="Primary"]').first
    box = rail.bounding_box()
    page.mouse.move(box["x"] + box["width"] / 2, box["y"] + 200)
    for _ in range(8):
        page.mouse.wheel(0, 400)
        page.wait_for_timeout(60)
    after = page.evaluate(
        "() => ({ top: document.documentElement.scrollTop,"
        "         rail: Math.round(document.querySelector('nav[aria-label=\\\"Primary\\\"]')"
        "                 .getBoundingClientRect().top) })"
    )
    check(
        "wheeling over the icon rail does not scroll the shell",
        after["top"] == 0 and after["rail"] == 0,
        f"documentElement.scrollTop={after['top']} railTop={after['rail']}",
    )

    # 3. A toggle list must actually collapse and expand.
    #
    # jsdom has no layout engine and no <details> semantics, so the vitest
    # suite can only assert that the NodeView and CSS exist. Whether clicking
    # the arrow hides the body is observable ONLY in a real browser, which is
    # the whole reason this harness exists.
    #
    # Before the fix there was no NodeView at all: nothing ever set `open`, and
    # ProseMirror's DOMObserver wiped the attribute if the browser set it, so
    # the toggle was permanently expanded.
    page.click(".note-editor-content .tiptap")
    page.keyboard.press("Control+End")
    page.keyboard.press("Enter")
    page.keyboard.type("/toggle")
    page.wait_for_timeout(400)
    page.keyboard.press("Enter")
    page.wait_for_timeout(300)

    details = page.locator(".note-editor-content .tiptap details").last
    check(
        "a toggle inserted from the slash menu starts expanded",
        details.evaluate("el => el.open") is True,
        "details.open=%s" % details.evaluate("el => el.open"),
    )
    check(
        "the toggle body starts as a bulleted list",
        details.locator("ul li").count() >= 1,
        "found %d list items" % details.locator("ul li").count(),
    )

    def body_height():
        return details.evaluate(
            "el => { const kids = [...el.children].filter(c => c.tagName !== 'SUMMARY');"
            "        return kids.reduce((h, c) => h + c.getBoundingClientRect().height, 0); }"
        )

    expanded_h = body_height()
    summary = details.locator("summary").first
    sbox = summary.bounding_box()
    # Click the ARROW, not the title: only the arrow zone toggles, so that
    # clicking the title can still place a caret to edit it.
    page.mouse.click(sbox["x"] + 8, sbox["y"] + sbox["height"] / 2)
    page.wait_for_timeout(250)
    collapsed_h = body_height()
    check(
        "clicking the arrow collapses the toggle body",
        details.evaluate("el => el.open") is False and collapsed_h < expanded_h,
        f"open={details.evaluate('el => el.open')} height {expanded_h} -> {collapsed_h}",
    )

    page.mouse.click(sbox["x"] + 8, sbox["y"] + sbox["height"] / 2)
    page.wait_for_timeout(250)
    check(
        "clicking the arrow again expands it",
        details.evaluate("el => el.open") is True and body_height() >= expanded_h,
        f"open={details.evaluate('el => el.open')} height={body_height()}",
    )

    # Clicking the TITLE must not toggle — otherwise the summary cannot be
    # edited without collapsing the thing you are reading.
    page.mouse.click(sbox["x"] + sbox["width"] - 12, sbox["y"] + sbox["height"] / 2)
    page.wait_for_timeout(250)
    check(
        "clicking the title places a caret instead of toggling",
        details.evaluate("el => el.open") is True,
        "open=%s" % details.evaluate("el => el.open"),
    )

    browser.close()

if failures:
    print("\n%d check(s) failed: %s" % (len(failures), ", ".join(failures)))
    sys.exit(1)
print("\nall checks passed")
