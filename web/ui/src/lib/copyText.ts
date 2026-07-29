// copyText is the app's single clipboard write.
//
// The non-secure-context path is the MAIN path here, not an exotic fallback: a
// self-hosted install is reached over plain HTTP on the LAN
// (http://<host>:8080), and `navigator.clipboard` only exists in a secure
// context (https, or localhost). Without the fallback the button does nothing.
//
// Extracted from MessageMeta so the connections wizard's redirect-URI copy
// button shares one implementation rather than growing a second, subtly
// different one.
export async function copyText(text: string): Promise<boolean> {
  try {
    // Optional-chained: in a non-secure context `navigator.clipboard` is
    // undefined, and reading `.writeText` off it throws before any copy is
    // attempted — which is exactly how this failed silently before.
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // permission denied, or a rejected write — fall through to the legacy path
  }
  return legacyCopy(text);
}

// document.execCommand("copy") is deprecated but is the only API that works
// without a secure context, and it copies the current SELECTION — hence the
// off-screen textarea, and hence restoring whatever the user had selected
// afterwards, so copying never eats their in-progress selection.
function legacyCopy(text: string): boolean {
  const ta = document.createElement("textarea");
  ta.value = text;
  // Off-screen rather than hidden: display:none / visibility:hidden makes the
  // node unselectable, so the copy silently yields an empty clipboard.
  ta.setAttribute("readonly", "");
  ta.style.position = "fixed";
  ta.style.top = "-9999px";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  const previous = document.getSelection()?.rangeCount ? document.getSelection()!.getRangeAt(0) : null;
  ta.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  document.body.removeChild(ta);
  if (previous) {
    const sel = document.getSelection();
    sel?.removeAllRanges();
    sel?.addRange(previous);
  }
  return ok;
}
