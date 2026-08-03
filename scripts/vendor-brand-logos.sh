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
moonshot:kimi-color
brave:brave-color
tavily:tavily-color
notion:notion
github:github
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
zoom:zoom-communications-logo
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
google_calendar:siGooglecalendar
google_tasks:siGoogletasks
todoist:siTodoist
home_assistant:siHomeassistant
immich:siImmich
paperless:siPaperlessngx
"

# strip_svg removes the XML prolog, DOCTYPE, comments, any <script>/<style>
# block, and the mark's own <title>, then collapses whitespace.
#
# Two reasons this matters, both because ProviderLogo INLINES these files:
#   - anything executable must not survive vendoring;
#   - a nested <title> becomes a second accessible name inside the tile, which
#     already carries role="img" + aria-label. Leaving it in makes the brand
#     name match twice in the accessibility tree.
strip_svg() {
  perl -0777 -pe '
    s/<\?xml.*?\?>//gs;
    s/<!DOCTYPE.*?>//gs;
    s/<!--.*?-->//gs;
    s/<script\b.*?<\/script>//gsi;
    s/<style\b.*?<\/style>//gsi;
    s/<title\b.*?<\/title>//gsi;
    s/\s+/ /g;
    s/^ //; s/ $//;
  ' "$1"
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

# linkedin_ads has no separate brand mark — it is the LinkedIn Marketing Developer
# Platform, not a distinct product — so it reuses LinkedIn's. Copied rather than
# symlinked because the SVGs are inlined by import.meta.glob at build time.
cp "$OUT/linkedin.svg" "$OUT/linkedin_ads.svg"
printf '  %-22s ← linkedin.svg (no distinct mark)\n' "linkedin_ads"

echo
echo "vendored $(ls -1 "$OUT"/*.svg | wc -l) logos into $OUT"
