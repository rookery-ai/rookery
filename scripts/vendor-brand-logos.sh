#!/usr/bin/env bash
# Vendor brand logos into web/ui/src/assets/logos/ as static SVG files.
#
# Run this only to add a brand or refresh the set — the generated SVGs are
# COMMITTED, so a normal build never touches the network and the app never
# loads a logo from a CDN.
#
# Three sources, in preference order:
#
#   lobehub  @lobehub/icons-static-svg (MIT) — the packaged form of the
#            lobehub.com/icons set. Authoritative for AI/LLM providers.
#            Where a "<name>-color" variant exists we take it; where only the
#            monochrome mark exists we take that, because for those brands
#            (OpenAI, Anthropic, GitHub, Notion…) monochrome IS the brand mark.
#            Mono files paint with fill="currentColor" and are rendered inline
#            by ProviderLogo, so they follow the app's light/dark theme.
#
#   wvl      worldvectorlogo's CDN — full-colour marks for the business brands
#            lobehub does not carry. Note their /search/ path 404s; asset URLs
#            are cdn.worldvectorlogo.com/logos/<their-slug>.svg and the slug is
#            pinned in the manifest below rather than discovered at run time.
#
#   simple   simple-icons, already a web/ui dependency — used only for the
#            three brands neither of the above carries. Emitted as a one-path
#            SVG in the brand's own hex.
#
#   upstream a pinned URL to the project's own published asset, for brands NONE of
#            the three sets above carry. Two shapes: a real .svg is stripped and
#            written as-is; a .png is wrapped in an <svg><image href="data:..."/>
#            because ProviderLogo INLINES these files, so every one must be an svg.
#            A full-colour raster needs no currentColor, so wrapping costs nothing.
#
# Logos are used nominatively to identify each integration. Files are kept
# byte-for-byte as published apart from the comment/metadata strip below.
set -euo pipefail

cd "$(dirname "$0")/.."
OUT="web/ui/src/assets/logos"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$OUT"

LOBEHUB_VERSION="1.94.0"
UA="Mozilla/5.0 (X11; Linux x86_64)"

# our-slug:lobehub-file
LOBEHUB="
openai:openai
anthropic:anthropic
openrouter:openrouter-color
zai:zhipu-color
ollama:ollama
ollama_local:ollama
deepseek:deepseek-color
groq:groq
xai:grok
mistral:mistral-color
gemini:gemini-color
google:google-color
perplexity:perplexity-color
# The MONO mark, not kimi-color: for this brand the "-color" variant is a white
# mark on a transparent field, drawn for Moonshot's own blue container, and
# ProviderLogo renders on a white tile — it showed an empty square with a speck
# in the corner. Mono paints with currentColor and the tile supplies contrast.
moonshot:kimi
brave:brave-color
tavily:tavily-color
notion:notion
github:github
# Wave 1 coder providers (2026-08). Verified present in
# @lobehub/icons-static-svg@1.94.0. nebius publishes only a monochrome mark (no
# -color variant) — that is its published form, themed via currentColor.
# github_models deliberately reuses the GitHub mark: it is a GitHub product.
# llama.cpp, LocalAI and Jan have no mark in ANY of the three sources and are
# exempted in web/logo_coverage_test.go's allowNoLogo instead.
bedrock:bedrock-color
alibaba:alibabacloud-color
together:together-color
fireworks:fireworks-color
cerebras:cerebras-color
sambanova:sambanova-color
nebius:nebius
deepinfra:deepinfra-color
huggingface:huggingface-color
github_models:github
lmstudio:lmstudio
vllm:vllm-color
# Wave 2 coder providers (2026-08). Each -color variant was screened for the
# white-on-transparent hazard that made Kimi render as an empty square: all six
# carry real brand colours. baseten and vercel publish only a monochrome mark,
# which is their published form and themes via currentColor.
# Chutes and LiteLLM have NO mark in lobehub or simple-icons, so neither
# provider ships in this wave — see the note in internal/coder/detect.go.
cohere:cohere-color
nvidia:nvidia-color
vercel_ai:vercel
minimax:minimax-color
baseten:baseten
novita:novita-color
hyperbolic:hyperbolic-color
venice:venice-color
# The AWS CONNECTOR (category Cloud), distinct from the bedrock coder provider
# above, which is a single AWS service. aws-color is the orange cube on a
# transparent field, which reads correctly on ProviderLogo's white tile.
# NOTE: no backticks anywhere in this block — it is inside a double-quoted shell
# string, so a backtick runs a command instead of being a comment.
aws:aws-color
# Wave 1 AI CONNECTORS (2026-08), which are a different thing from the coder
# providers above: these are services an agent calls with the user's own key.
# anthropic, openrouter, perplexity and huggingface are already vendored above
# and reuse the same slug, so only these two are new. replicate takes the mono
# mark, its published form; assemblyai-color is blue and reads on the white
# tile. deepgram has no lobehub mark and comes from simple-icons below.
replicate:replicate
assemblyai:assemblyai-color
# Wave 2 CLOUD-ADJACENT connectors (2026-08). vercel reuses the same mark as the
# vercel_ai coder provider above — one brand, two integrations. cloudflare-color
# is orange and reads on the white tile. DigitalOcean, Netlify, Fly.io and
# Hetzner come from simple-icons below; Linode from worldvectorlogo, the only
# one of the seven that neither AI-oriented set carries.
cloudflare:cloudflare-color
vercel:vercel
"

# our-slug:worldvectorlogo-slug
WVL="
telegram:telegram-1
discord:discord-6
slack:slack-new-logo
airtable:airtable-1
asana:asana-logo
calendly:calendly
clickup:clickup
dropbox:dropbox
google_drive:google-drive
hubspot:hubspot-1
intercom:intercom-2
jira:jira-1
mailchimp:mailchimp-freddie-icon
monday:monday-1
outlook:microsoft-office-outlook
salesforce:salesforce-2
sendgrid:sendgrid-1
shopify:shopify
stripe:stripe-4
teams:microsoft-teams-1
trello:trello
twilio:twilio-2
zendesk:zendesk-1
# zoom deliberately absent: the provider was removed (its connect flow could not
# be completed against a real account) and its logo deleted in the same commit,
# but this manifest line was left behind — so the next re-vendor silently
# recreated zoom.svg. Pinned by connectors.TestRemovedProvidersStayRemoved.
linkedin:linkedin-icon-2
youtube:youtube-2
facebook:facebook-4
instagram:instagram-2016-5
meta_ads:meta-3
google_analytics:google-analytics-4
google_adsense:google-adsense
google_searchconsole:google-search-console
google_ads:google-ads-2
tiktok:tiktok-icon-2
reddit:reddit-4
mastodon:mastodon-2
# Linode (wave 2) — the only cloud-adjacent brand carried by neither lobehub nor
# simple-icons.
linode:linode
"

# our-slug:simple-icons-export
SIMPLE="
google_docs:siGoogledocs
google_sheets:siGooglesheets
opencode_zen:siOpencode
opencode_go:siOpencode
# These four brands are MONOCHROME by design — X and Threads are black, Pinterest
# red, Bluesky blue — so a single-path simple-icons mark is the accurate rendering,
# not a degraded one. Everything else moved to worldvectorlogo for full colour.
x:siX
threads:siThreads
pinterest:siPinterest
bluesky:siBluesky
# Everyday-connector providers. simple-icons carries all six of these; YNAB,
# Raindrop.io and Open-Meteo have no mark in ANY of the three sources and are
# exempted in web/logo_coverage_test.go's allowNoLogo instead.
# Wave 4. Open Library, Open Food Facts, Gotify, Linkwarden and Oura have no mark in
# any source and are exempted in web/logo_coverage_test.go instead.
openstreetmap:siOpenstreetmap
nextcloud:siNextcloud
mealie:siMealie
vikunja:siVikunja
portainer:siPortainer
# fitbit deliberately absent: replaced by google_health (Fitbit's Web API is
# decommissioned in September 2026 along with its OAuth server). Its logo was
# deleted with the provider, but this manifest line survived and recreated
# fitbit.svg on the next re-vendor. Pinned by
# connectors.TestRemovedProvidersStayRemoved.
spotify:siSpotify
trakt:siTrakt
# Wave 3 — the homelab stack plus a few cloud services. All have marks.
sonarr:siSonarr
radarr:siRadarr
grafana:siGrafana
n8n:siN8n
gitea:siGitea
karakeep:siKarakeep
audiobookshelf:siAudiobookshelf
changedetection:siChangedetection
syncthing:siSyncthing
steam:siSteam
lastfm:siLastdotfm
clockify:siClockify
wakatime:siWakatime
# Wave 2. Readwise, Miniflux and Frankfurter have no mark in any source and are
# exempted in web/logo_coverage_test.go instead.
ntfy:siNtfy
toggl:siToggl
jellyfin:siJellyfin
# Deepgram (wave 1 AI connectors) — the one brand in that wave with no lobehub
# mark. simple-icons carries it.
deepgram:siDeepgram
# Wave 2 cloud-adjacent connectors. Note the export name for Fly.io is
# siFlydotio, not siFly — siFly is a different brand entirely.
digitalocean:siDigitalocean
netlify:siNetlify
flyio:siFlydotio
hetzner:siHetzner
# Wave 3 money connectors. CoinGecko and Alpha Vantage are in none of the three
# sets and take the upstream path below instead.
wise:siWise
# Wave 4 notification and email connectors. Pushover has no mark in any set and
# takes the upstream path.
pushbullet:siPushbullet
resend:siResend
mailgun:siMailgun
matrix:siMatrix
adguard:siAdguard
firefly_iii:siFireflyiii
tmdb:siThemoviedatabase
wikipedia:siWikipedia
strava:siStrava
google_health:siGoogle
google_calendar:siGooglecalendar
google_tasks:siGoogletasks
todoist:siTodoist
home_assistant:siHomeassistant
immich:siImmich
paperless:siPaperlessngx
"

# inline_class_styles resolves simple class-based paint rules into presentation
# attributes BEFORE strip_svg removes the <style> block that defined them.
#
# This is not a nicety. Illustrator and Inkscape both export marks as
# `<rect class="st2"/>` plus a `<style>.st2{fill:#1b1f20}</style>`, and
# strip_svg has to remove that <style> (these files are inlined into the DOM
# with dangerouslySetInnerHTML). Without the rule, every classed element falls
# back to the SVG default `fill: black`:
#
#   - llama.cpp rendered as a SOLID BLACK SQUARE — its background <rect> is
#     .st2 and the llama itself .st0, so the plate swallowed the mark entirely.
#   - Open Library lost three stroke paths — .st0 is `fill:none;stroke:...`, so
#     they became zero-area black and vanished. That one looked merely plain
#     rather than broken, which is why it sat unnoticed.
#
# Both passed every structural test: they start with <svg>, carry a viewBox,
# have no <script> and no <title>. Only rendering them reveals it. Handling it
# here means the next Illustrator export is fixed on arrival instead of
# becoming a third per-brand patch.
#
# Deliberately simple: single-class selectors and paint properties only. A rule
# it cannot express is left alone rather than half-applied.
inline_class_styles() {
  python3 - "$1" <<'PY'
import re, sys

PAINT = {
    "fill", "stroke", "stroke-width", "stroke-linecap", "stroke-linejoin",
    "stroke-miterlimit", "stroke-dasharray", "opacity", "fill-opacity",
    "stroke-opacity", "fill-rule",
}


def drop_at_blocks(css):
    """Remove @media/@supports blocks, braces balanced.

    Their rules must NOT be applied unconditionally. Frankfurter ships a
    `@media (prefers-color-scheme: dark)` block that repaints its mark for a
    dark background; ProviderLogo always renders on a WHITE tile, so the
    light-mode rules are the correct ones and the dark ones would invert it.
    A presentation attribute cannot express a media query either way.
    """
    out, i = [], 0
    while i < len(css):
        if css[i] == "@":
            depth, j = 0, i
            while j < len(css):
                if css[j] == "{":
                    depth += 1
                elif css[j] == "}":
                    depth -= 1
                    if depth == 0:
                        j += 1
                        break
                j += 1
            i = j
            continue
        out.append(css[i])
        i += 1
    return "".join(out)


src = open(sys.argv[1]).read()
rules = {}
for block in re.findall(r"<style[^>]*>(.*?)</style>", src, re.S | re.I):
    for sel, body in re.findall(r"([^{}]+)\{([^{}]*)\}", drop_at_blocks(block)):
        decls = {}
        for decl in body.split(";"):
            if ":" not in decl:
                continue
            prop, _, val = decl.partition(":")
            prop, val = prop.strip().lower(), val.strip()
            # Paint properties only. Anything else (transforms, filters,
            # animation) is out of scope and safer left unapplied.
            if prop in PAINT and val:
                decls[prop] = val
        if not decls:
            continue
        # Selector LISTS matter: frankfurter writes `.p, .s { fill: #1a1a1a }`,
        # and reading only the last class would leave .p unpainted (i.e. black).
        # Anything that is not a bare single class — descendant, attribute or
        # element selectors — is skipped rather than guessed at.
        for one in sel.split(","):
            one = one.strip()
            if re.fullmatch(r"\.[A-Za-z_][\w-]*", one):
                rules.setdefault(one[1:], {}).update(decls)

if rules:
    def sub(m):
        attrs = {}
        for n in m.group(1).split():
            attrs.update(rules.get(n, {}))
        if not attrs:
            return m.group(0)
        return " ".join(f'{k}="{v}"' for k, v in attrs.items())

    src = re.sub(r'class="([^"]*)"', sub, src)

sys.stdout.write(src)
PY
}

# strip_svg removes the XML prolog, DOCTYPE, comments, any <script>/<style>
# block, and the mark's own <title>, then collapses whitespace. Class-based
# paint rules are inlined first (see above) so removing <style> cannot silently
# repaint the mark black.
#
# Two reasons the stripping matters, both because ProviderLogo INLINES these
# files:
#   - anything executable must not survive vendoring;
#   - a nested <title> becomes a second accessible name inside the tile, which
#     already carries role="img" + aria-label. Leaving it in makes the brand
#     name match twice in the accessibility tree.
strip_svg() {
  inline_class_styles "$1" | perl -0777 -pe '
    s/<\?xml.*?\?>//gs;
    s/<!DOCTYPE.*?>//gs;
    s/<!--.*?-->//gs;
    s/<script\b.*?<\/script>//gsi;
    s/<style\b.*?<\/style>//gsi;
    s/<title\b.*?<\/title>//gsi;
    s/\s+/ /g;
    s/^ //; s/ $//;
  '
}

echo "→ lobehub (@lobehub/icons-static-svg@$LOBEHUB_VERSION)"
( cd "$TMP" && npm pack "@lobehub/icons-static-svg@$LOBEHUB_VERSION" >/dev/null 2>&1 \
  && tar xzf "lobehub-icons-static-svg-$LOBEHUB_VERSION.tgz" )
echo "$LOBEHUB" | while read -r pair; do
  case "$pair" in ""|"#"*) continue ;; esac
  ours="${pair%%:*}"; theirs="${pair##*:}"
  src="$TMP/package/icons/$theirs.svg"
  [ -f "$src" ] || { echo "  MISSING lobehub:$theirs" >&2; exit 1; }
  strip_svg "$src" > "$OUT/$ours.svg"
  printf '  %-14s ← %s\n' "$ours" "$theirs"
done

echo "→ worldvectorlogo"
echo "$WVL" | while read -r pair; do
  case "$pair" in ""|"#"*) continue ;; esac
  ours="${pair%%:*}"; theirs="${pair##*:}"
  code=$(curl -sSL -A "$UA" -o "$TMP/dl.svg" -w "%{http_code}" \
    "https://cdn.worldvectorlogo.com/logos/$theirs.svg")
  [ "$code" = "200" ] || { echo "  FAILED $theirs (HTTP $code)" >&2; exit 1; }
  head -c 5 "$TMP/dl.svg" | grep -qi "<svg\|<?xml" || { echo "  NOT SVG: $theirs" >&2; exit 1; }
  strip_svg "$TMP/dl.svg" > "$OUT/$ours.svg"
  printf '  %-14s ← %s\n' "$ours" "$theirs"
done

echo "→ simple-icons"
echo "$SIMPLE" | while read -r pair; do
  case "$pair" in ""|"#"*) continue ;; esac
  ours="${pair%%:*}"; theirs="${pair##*:}"
  # run from web/ui so node resolves simple-icons out of the SPA's node_modules.
  # No <title> and no role: the tile that inlines this already supplies both.
  ( cd web/ui && node --input-type=module -e "
      import { $theirs as i } from 'simple-icons';
      process.stdout.write(
        '<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 24 24\">' +
        '<path fill=\"#' + i.hex + '\" d=\"' + i.path + '\"/></svg>'
      );
    " ) > "$OUT/$ours.svg"
  printf '  %-14s ← %s\n' "$ours" "$theirs"
done

# ── upstream ────────────────────────────────────────────────────────────────
# our-slug|url   — brands no other source carries. Verified reachable 2026-08-04.
UPSTREAM_SVG="
hackernews|https://news.ycombinator.com/y18.svg
openlibrary|https://openlibrary.org/static/images/openlibrary-logo-tighter.svg
frankfurter|https://frankfurter.dev/images/logo.svg
gotify|https://raw.githubusercontent.com/gotify/logo/master/gotify-logo-small.svg
# llama.cpp publishes a real square svg in its own repo. It is in none of the
# three sources above, which is why it was exempted in
# web/logo_coverage_test.go until 2026-08-05.
llamacpp|https://raw.githubusercontent.com/ggml-org/llama.cpp/master/media/llama1-icon.svg
"

# Raster-only brands: no vector mark is published anywhere we can reach.
UPSTREAM_PNG="
ynab|https://cdn.prod.website-files.com/640f69143ec11b21d42015c6/6732b255e4999d561f33e7bb_Frame%202%20(1).png
raindrop|https://raindrop.io/_next/static/media/icon_128.6bddf89e.png
oura|https://ouraring.com/assets/icons/apple-touch-icon.png
open_meteo|https://open-meteo.com/apple-touch-icon.png
linkwarden|https://linkwarden.app/apple-touch-icon.png
openfoodfacts|https://world.openfoodfacts.org/images/favicon/off/apple-touch-icon.png
# Readwise: readwise.io/favicon.ico and every /static/ path 403 behind their CDN
# challenge, which is why this was exempted rather than vendored. Their home page
# serves 200 to a browser UA and links this apple-touch-icon on a CDN host that
# answers unchallenged. The URL embeds a content hash, so it will change when
# they redeploy — a re-vendor run then fails loudly while the committed SVG keeps
# rendering, which is the right way round.
readwise|https://d34adp677peecb.cloudfront.net/static/images/favicons/apple-touch-icon.8284936de99b.png
# Wave 3 money connectors. Neither brand is in lobehub, simple-icons or
# worldvectorlogo, so both take their own published raster.
coingecko|https://www.coingecko.com/favicon-96x96.png
pushover|https://pushover.net/apple-touch-icon.png
"

echo "→ upstream (svg)"
echo "$UPSTREAM_SVG" | while read -r pair; do
  case "$pair" in ""|"#"*) continue ;; esac
  ours="${pair%%|*}"; url="${pair#*|}"
  curl -fsSL -A "$UA" "$url" -o "$TMP/$ours.raw.svg" || { echo "  !! $ours failed"; continue; }
  strip_svg "$TMP/$ours.raw.svg" > "$OUT/$ours.svg"
  printf '  %-14s ← %s\n' "$ours" "$url"
done

echo "→ upstream (png → inlined svg)"
echo "$UPSTREAM_PNG" | while read -r pair; do
  case "$pair" in ""|"#"*) continue ;; esac
  ours="${pair%%|*}"; url="${pair#*|}"
  curl -fsSL -A "$UA" "$url" -o "$TMP/$ours.png" || { echo "  !! $ours failed"; continue; }
  python3 - "$TMP/$ours.png" "$OUT/$ours.svg" <<'PY'
import base64, re, subprocess, sys
src, dst = sys.argv[1], sys.argv[2]
info = subprocess.run(["file", "-b", src], capture_output=True, text=True).stdout
m = re.search(r"(\d+) x (\d+)", info)
w, h = (m.group(1), m.group(2)) if m else ("256", "256")
data = base64.b64encode(open(src, "rb").read()).decode()
open(dst, "w").write(
    f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}">'
    f'<image href="data:image/png;base64,{data}" width="{w}" height="{h}"/></svg>'
)
PY
  printf '  %-14s ← %s\n' "$ours" "$url"
done

# Oversized square rasters: downscaled before wrapping, because these files are
# INLINED into the DOM and every byte is paid on render.
#
# LocalAI is the one that forced this. Its favicon.svg is a genuine square
# vector, but a 41-path illustration at ~110 KB — 78 KB even after trimming
# coordinate precision, which alone grew the ProviderLogo chunk from 286 KB to
# 376 KB. Its logo.png is the same mark at 1024x1024 and downscales to ~17 KB.
# ProviderLogo renders a 32-48 px tile, so 128 px is still 3-4x the rendered
# size and stays crisp on a hi-DPI display.
UPSTREAM_PNG_LARGE="
localai|https://raw.githubusercontent.com/mudler/LocalAI/master/core/http/static/logo.png
"

echo "→ upstream (large png → downscaled + inlined svg)"
echo "$UPSTREAM_PNG_LARGE" | while read -r pair; do
  case "$pair" in ""|"#"*) continue ;; esac
  ours="${pair%%|*}"; url="${pair#*|}"
  curl -fsSL -A "$UA" "$url" -o "$TMP/$ours.big.png" || { echo "  !! $ours failed"; continue; }
  python3 - "$TMP/$ours.big.png" "$OUT/$ours.svg" <<'PY'
import base64, io, sys
from PIL import Image
src, dst = sys.argv[1], sys.argv[2]
im = Image.open(src).convert("RGBA")
im.thumbnail((128, 128), Image.LANCZOS)
buf = io.BytesIO(); im.save(buf, "PNG", optimize=True)
data = base64.b64encode(buf.getvalue()).decode()
open(dst, "w").write(
    f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {im.width} {im.height}">'
    f'<image href="data:image/png;base64,{data}" width="{im.width}" height="{im.height}"/></svg>'
)
PY
  printf '  %-14s ← %s\n' "$ours" "$url"
done

# Miniflux publishes no PNG or SVG we can reach — only a favicon.ico — so its largest
# frame is re-encoded as PNG and wrapped like the rasters above.
UPSTREAM_ICO="
miniflux|https://reader.miniflux.app/favicon.ico
# Jan publishes no svg or png we can reach — only this favicon.
jan|https://jan.ai/favicon.ico
# Alpha Vantage (wave 3) publishes nothing else either.
alphavantage|https://www.alphavantage.co/static/img/favicon.ico
"

echo "→ upstream (ico → inlined svg)"
echo "$UPSTREAM_ICO" | while read -r pair; do
  case "$pair" in ""|"#"*) continue ;; esac
  ours="${pair%%|*}"; url="${pair#*|}"
  curl -fsSL -A "$UA" "$url" -o "$TMP/$ours.ico" || { echo "  !! $ours failed"; continue; }
  python3 - "$TMP/$ours.ico" "$OUT/$ours.svg" <<'PY'
import base64, io, sys
from PIL import Image
src, dst = sys.argv[1], sys.argv[2]
im = Image.open(src)
best = max(im.info.get("sizes", [im.size]))
im = Image.open(src); im.size = best; im.load()
im = im.convert("RGBA")
buf = io.BytesIO(); im.save(buf, "PNG", optimize=True)
data = base64.b64encode(buf.getvalue()).decode()
open(dst, "w").write(
    f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {im.width} {im.height}">'
    f'<image href="data:image/png;base64,{data}" width="{im.width}" height="{im.height}"/></svg>'
)
PY
  printf '  %-14s ← %s\n' "$ours" "$url"
done

# linkedin_ads has no separate brand mark — it is the LinkedIn Marketing Developer
# Platform, not a distinct product — so it reuses LinkedIn's. Copied rather than
# symlinked because the SVGs are inlined by import.meta.glob at build time.
cp "$OUT/linkedin.svg" "$OUT/linkedin_ads.svg"
printf '  %-22s ← linkedin.svg (no distinct mark)\n' "linkedin_ads"

echo
echo "vendored $(ls -1 "$OUT"/*.svg | wc -l) logos into $OUT"
