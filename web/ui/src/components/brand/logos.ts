// Curated brand-icon map for ProviderLogo. Slugs cover:
//   - chat platforms (internal/gateway CredSpecs): telegram, discord, slack
//   - service-connector providers (internal/connectors/providers/*.yaml, 28 files)
//   - AI/coder API providers (internal/coder.APIProviders() Name field)
//
// Icons are imported individually from `simple-icons` (side-effect-free named
// exports — the bundler tree-shakes unused ones) so only icons we actually
// reference end up in the production bundle.
//
// Some brands have been REMOVED from simple-icons entirely (typically after a
// trademark/legal takedown request) and cannot be imported at all in this
// installed version (16.26.0): slack, openai, outlook, teams (Microsoft
// Teams), salesforce, sendgrid, twilio, groq, xai (Grok), monday (monday.com).
// Those slugs are intentionally OMITTED from PROVIDER_LOGOS below — the
// ProviderLogo component's initial+color fallback covers them.
import {
  siAirtable,
  siAnthropic,
  siAsana,
  siCalendly,
  siClickup,
  siDeepseek,
  siDiscord,
  siDropbox,
  siGithub,
  siGoogle,
  siGoogledocs,
  siGoogledrive,
  siGooglegemini,
  siGooglesheets,
  siHubspot,
  siIntercom,
  siJira,
  siMailchimp,
  siMistralai,
  siMoonshotai,
  siNotion,
  siOllama,
  siOpencode,
  siOpenrouter,
  siPerplexity,
  siShopify,
  siStripe,
  siTelegram,
  siTrello,
  siZdotai,
  siZendesk,
  siZoom,
} from "simple-icons";

export type LogoEntry = { path: string; hex: string; title: string };

function entry(icon: { path: string; hex: string; title: string }): LogoEntry {
  return { path: icon.path, hex: icon.hex, title: icon.title };
}

export const PROVIDER_LOGOS: Record<string, LogoEntry> = {
  // Chat platforms
  telegram: entry(siTelegram),
  discord: entry(siDiscord),
  // slack: no icon in this simple-icons version — fallback

  // Service-connector providers
  airtable: entry(siAirtable),
  asana: entry(siAsana),
  calendly: entry(siCalendly),
  clickup: entry(siClickup),
  dropbox: entry(siDropbox),
  github: entry(siGithub),
  google: entry(siGoogle),
  google_docs: entry(siGoogledocs),
  google_drive: entry(siGoogledrive),
  google_sheets: entry(siGooglesheets),
  hubspot: entry(siHubspot),
  intercom: entry(siIntercom),
  jira: entry(siJira),
  mailchimp: entry(siMailchimp),
  // monday: no icon — fallback
  notion: entry(siNotion),
  // openai: no icon — fallback (also covers the "openai" AI-provider slug)
  // outlook: no icon — fallback
  // salesforce: no icon — fallback
  // sendgrid: no icon — fallback
  shopify: entry(siShopify),
  stripe: entry(siStripe),
  // teams: no icon — fallback
  trello: entry(siTrello),
  // twilio: no icon — fallback
  zendesk: entry(siZendesk),
  zoom: entry(siZoom),

  // AI / coder API providers
  anthropic: entry(siAnthropic),
  openrouter: entry(siOpenrouter),
  zai: entry(siZdotai),
  ollama: entry(siOllama),
  ollama_local: entry(siOllama),
  deepseek: entry(siDeepseek),
  // groq: no icon — fallback
  // xai: no icon — fallback
  mistral: entry(siMistralai),
  gemini: entry(siGooglegemini),
  opencode_zen: entry(siOpencode),
  opencode_go: entry(siOpencode),
  perplexity: entry(siPerplexity),
  moonshot: entry(siMoonshotai),
  // generic: "Custom (OpenAI-compatible)" has no brand — fallback
};
