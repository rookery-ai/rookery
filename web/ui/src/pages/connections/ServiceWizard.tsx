import { useState } from "react";
import type { ReactNode } from "react";
import { AlertTriangle, Link2, RefreshCw, Save, Unlink } from "lucide-react";
import { useSlideOver } from "@/components/shell/AppShell";
import { PanelBody } from "@/components/shell/PanelBody";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError } from "@/lib/api";
import { Linkify } from "@/lib/linkify";
import { CopyButton } from "@/components/CopyButton";
import {
  useServices,
  useSaveProviderCreds,
  useConnectService,
  useConnectAPIKey,
  useDeleteServiceConnection,
  type ServiceProvider,
  type ServiceConnection,
} from "@/lib/connections";
import { ProviderActions } from "./ProviderActions";

type ServiceWizardProps = { provider: ServiceProvider };

const REDIRECT_TOKEN = "{{redirect_uri}}";

// SetupStep renders one provider instruction, replacing {{redirect_uri}} with
// the real URI as copyable code.
//
// The surrounding prose still goes through Linkify so console links keep
// working; the URI itself deliberately does NOT become a link. It is a value to
// paste into another site, not a destination to visit — and following it would
// hit our own callback route with no state parameter, which only ever renders
// "Invalid or expired authorization request".
function SetupStep({
  text,
  redirectURI,
}: {
  text: string;
  redirectURI: string;
}) {
  if (!redirectURI || !text.includes(REDIRECT_TOKEN)) {
    return <Linkify text={text} />;
  }
  const parts = text.split(REDIRECT_TOKEN);
  return (
    <>
      {parts.map((part, i) => (
        <span key={i}>
          <Linkify text={part} />
          {i < parts.length - 1 && (
            <span className="inline-flex items-baseline gap-1 align-baseline">
              <code className="break-all rounded bg-muted-surface px-1 py-0.5 text-xs">
                {redirectURI}
              </code>
              <CopyButton value={redirectURI} />
            </span>
          )}
        </span>
      ))}
    </>
  );
}

function ErrorNote({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
      <AlertTriangle className="size-3.5 shrink-0" />
      {children}
    </div>
  );
}

function errMsg(err: unknown) {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

// ── Connected accounts (top) ────────────────────────────────────────────────

function AccountRow({
  connection,
  onReconnect,
}: {
  connection: ServiceConnection;
  onReconnect: () => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const deleteMutation = useDeleteServiceConnection();

  async function handleDisconnect() {
    setError(null);
    try {
      await deleteMutation.mutateAsync(connection.id);
      setConfirming(false);
    } catch (err) {
      setError(errMsg(err));
    }
  }

  const active = connection.status === "ACTIVE";

  return (
    <div className="space-y-2 rounded-xl border border-border p-4 text-sm">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate font-medium">{connection.label}</div>
          {connection.identity && (
            <div className="truncate text-xs text-muted-2">
              {connection.identity}
            </div>
          )}
        </div>
        {active ? (
          <span className="flex shrink-0 items-center gap-1 text-xs font-medium text-ok">
            <span className="size-1.5 rounded-full bg-ok" /> Connected
          </span>
        ) : (
          <span className="flex shrink-0 items-center gap-1 text-xs font-medium text-warn">
            <span className="size-1.5 rounded-full bg-warn" /> needs reconnect
          </span>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {!active && (
          <Button size="sm" variant="outline" onClick={onReconnect}>
            <RefreshCw />
            Reconnect
          </Button>
        )}
        {!confirming ? (
          <Button
            size="sm"
            variant="outline"
            className="text-danger"
            onClick={() => setConfirming(true)}
          >
            <Unlink />
            Disconnect
          </Button>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs text-danger">
              Disconnect {connection.label}?
            </span>
            <Button
              size="sm"
              variant="outline"
              onClick={() => setConfirming(false)}
            >
              Cancel
            </Button>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => void handleDisconnect()}
              disabled={deleteMutation.isPending}
            >
              <Unlink />
              Yes, disconnect
            </Button>
          </div>
        )}
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
    </div>
  );
}

// ── Entry point ──────────────────────────────────────────────────────────────
// Title convention (decided here, mirrors ChatAppWizard): the OPENER
// (ConnectionsPage) sets the slide-over title to "Connect <label>" when the
// provider has no connections yet, "Manage <label>" once it has at least
// one — same rule the chat-app wizard uses for its connected/not-connected
// split.

export function ServiceWizard({
  provider: initialProvider,
}: ServiceWizardProps) {
  const { close } = useSlideOver();

  // Prefer live data — after a connect/disconnect/reconnect mutation
  // invalidates the ["services"] query, this refetches and the connected
  // accounts list updates in place without closing the panel (a provider can
  // have many accounts, unlike a chat-app platform's single connection).
  // Fall back to the snapshot the caller opened the panel with while that
  // first fetch is in flight, or if the provider ever drops out of the list.
  const servicesQuery = useServices();
  const provider =
    servicesQuery.data?.providers.find(
      (p) => p.name === initialProvider.name,
    ) ?? initialProvider;

  const [view, setView] = useState<"creds" | "connect">(
    provider.has_creds ? "connect" : "creds",
  );
  // A hard preflight problem is provably fatal for this provider, so Connect is
  // disabled rather than letting the user walk into a provider error screen.
  // Soft problems only warn — see the never-lock-anyone-out rule in publicurl.
  const hardBlocked = provider.preflight.some((p) => p.severity === "hard");
  // Overlays `view` rather than widening its union: `view` keeps meaning "which
  // connect step am I on", so Back lands where the user left without a separate
  // variable remembering it. ServiceWizard stays mounted throughout, so every
  // form field below survives the round trip — which a second slide-over panel
  // could not have done (the shell's slide-over is a single slot).
  const [showActions, setShowActions] = useState(false);
  const [label, setLabel] = useState("");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [inputs, setInputs] = useState<Record<string, string>>({});
  const [credsError, setCredsError] = useState<string | null>(null);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [keyError, setKeyError] = useState<string | null>(null);

  const saveCredsMutation = useSaveProviderCreds();
  const connectServiceMutation = useConnectService();
  const connectAPIKeyMutation = useConnectAPIKey();

  // Reconnect must actually re-authenticate. It used to only switch the view and
  // seed the label, so the button labelled Reconnect filled in a text field and
  // stopped.
  //
  // Only an OAuth provider has a consent URL to send the user to. An api_key
  // provider has nothing to redirect to, and an OAuth provider with required
  // connect_inputs needs values we must not guess — Google Ads collects a
  // developer token, which is a secret we should not echo back into a form the
  // user did not ask for. Both land on the form, which is correct and is what
  // the old code already did for every case.
  function reconnect(seedLabel: string) {
    setView("connect");
    setLabel(seedLabel);
    const needsInput = provider.connect_inputs.some((i) => i.required);
    if (provider.kind === "oauth" && !needsInput) {
      void handleConnect(seedLabel);
    }
  }

  async function handleSaveCreds() {
    setCredsError(null);
    try {
      await saveCredsMutation.mutateAsync({
        provider: provider.name,
        clientId,
        clientSecret,
      });
      setView("connect");
    } catch (err) {
      setCredsError(errMsg(err));
    }
  }

  // The label is a parameter, not read from state: reconnect() calls this
  // immediately after setLabel(), and setLabel is async — reading state here
  // would send the PREVIOUS label. That matters beyond cosmetics, because the
  // label is the upsert key: InsertServiceConnection conflicts on
  // (workspace_id, provider, account_label), so a wrong one creates a SECOND
  // connection and leaves the broken one still bound to the user's agents.
  async function handleConnect(labelOverride?: string) {
    setConnectError(null);
    try {
      const res = await connectServiceMutation.mutateAsync({
        provider: provider.name,
        label: labelOverride ?? label,
        inputs,
      });
      window.location.assign(res.redirect_url);
    } catch (err) {
      setConnectError(errMsg(err));
    }
  }

  async function handleConnectAPIKey() {
    setKeyError(null);
    try {
      await connectAPIKeyMutation.mutateAsync({
        provider: provider.name,
        key: apiKey,
        label,
        inputs,
      });
      close();
    } catch (err) {
      setKeyError(errMsg(err));
    }
  }

  const hasConnections = provider.connections.length > 0;

  if (showActions) {
    return (
      <PanelBody>
        <ProviderActions
          provider={provider}
          onBack={() => setShowActions(false)}
        />
      </PanelBody>
    );
  }

  return (
    <PanelBody>
      {provider.action_count > 0 && (
        <button
          type="button"
          onClick={() => setShowActions(true)}
          className="flex w-full items-center justify-between gap-2 rounded-lg border border-border px-3 py-2 text-sm transition-colors hover:border-primary/40"
        >
          <span className="font-medium">What can it do?</span>
          <span className="shrink-0 text-xs text-muted-2">
            {provider.action_count} action
            {provider.action_count === 1 ? "" : "s"} →
          </span>
        </button>
      )}

      {hasConnections && (
        <div className="space-y-2">
          <div className="text-xs font-semibold uppercase tracking-wide text-muted-2">
            Connected accounts
          </div>
          {provider.connections.map((c) => (
            <AccountRow
              key={c.id}
              connection={c}
              onReconnect={() => reconnect(c.label)}
            />
          ))}
        </div>
      )}

      {/* Shown on BOTH the credentials step and the connect step: the URI is
          needed while registering the app, and the preflight verdict has to be
          visible next to the Connect button it disables. */}
      {provider.kind === "oauth" && provider.redirect_uri && (
        <div className="space-y-1 rounded-lg border border-border bg-muted-surface p-3">
          <div className="text-xs font-semibold uppercase tracking-wide text-muted-2">
            Redirect URI to register
          </div>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 break-all text-xs">
              {provider.redirect_uri}
            </code>
            <CopyButton value={provider.redirect_uri} />
          </div>
        </div>
      )}

      {provider.kind === "oauth" &&
        provider.preflight.map((p) => (
          <div
            key={p.code}
            className={
              p.severity === "hard"
                ? "space-y-1 rounded-lg border border-danger/40 bg-danger-soft p-3 text-sm"
                : "space-y-1 rounded-lg border border-border bg-muted-surface p-3 text-sm"
            }
          >
            <div className="font-medium">{p.message}</div>
            <div className="text-muted-2">{p.fix}</div>
          </div>
        ))}

      <div
        className={
          hasConnections ? "space-y-3 border-t border-border pt-4" : "space-y-3"
        }
      >
        {provider.kind === "keyless" ? (
          <div className="space-y-3">
            {/* Setup steps render as plain text, not through SetupStep: that
                component substitutes {{redirect_uri}}, and a keyless provider
                has none. */}
            {provider.setup_steps.length > 0 && (
              <ol className="space-y-2">
                {provider.setup_steps.map((s, i) => (
                  <li
                    key={i}
                    className="flex gap-3 rounded-lg border border-border bg-background p-3 text-sm"
                  >
                    <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-muted-surface text-xs font-semibold">
                      {i + 1}
                    </span>
                    <span className="leading-relaxed">{s}</span>
                  </li>
                ))}
              </ol>
            )}
            {keyError && <ErrorNote>{keyError}</ErrorNote>}
            <div className="flex justify-end">
              <Button
                onClick={() => void handleConnectAPIKey()}
                disabled={connectAPIKeyMutation.isPending}
              >
                <Link2 />
                {connectAPIKeyMutation.isPending
                  ? "Connecting…"
                  : `Connect ${provider.label}`}
              </Button>
            </div>
          </div>
        ) : provider.kind === "oauth" ? (
          view === "creds" ? (
            <div className="space-y-3">
              {provider.setup_url && (
                <a
                  href={provider.setup_url}
                  target="_blank"
                  rel="noreferrer"
                  className="block rounded-md border border-primary/30 bg-primary/5 px-3 py-2 text-sm font-medium text-primary underline underline-offset-2"
                >
                  {provider.setup_url}
                </a>
              )}
              {provider.setup_steps.length > 0 && (
                <ol className="space-y-2">
                  {provider.setup_steps.map((s, i) => (
                    <li
                      key={i}
                      className="flex gap-3 rounded-lg border border-border bg-background p-3 text-sm"
                    >
                      <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-muted-surface text-xs font-semibold">
                        {i + 1}
                      </span>
                      <span className="leading-relaxed">
                        <SetupStep
                          text={s}
                          redirectURI={provider.redirect_uri}
                        />
                      </span>
                    </li>
                  ))}
                </ol>
              )}
              <div className="space-y-1">
                <Label htmlFor="svc-client-id">
                  {provider.oauth_creds?.id_label || "Client ID"}
                </Label>
                <Input
                  id="svc-client-id"
                  value={clientId}
                  onChange={(e) => setClientId(e.target.value)}
                  autoComplete="off"
                />
                {provider.oauth_creds?.id_hint && (
                  <p className="text-xs text-muted-2">{provider.oauth_creds.id_hint}</p>
                )}
              </div>
              <div className="space-y-1">
                <Label htmlFor="svc-client-secret">
                  {provider.oauth_creds?.secret_label || "Client secret"}
                </Label>
                <Input
                  id="svc-client-secret"
                  type="password"
                  value={clientSecret}
                  onChange={(e) => setClientSecret(e.target.value)}
                  // Not "off": Chrome ignores that on password fields and
                  // pairs them with a nearby text input it fills as the
                  // username. "new-password" opts out of the pairing.
                  autoComplete="new-password"
                />
                {provider.oauth_creds?.secret_hint && (
                  <p className="text-xs text-muted-2">{provider.oauth_creds.secret_hint}</p>
                )}
              </div>
              {credsError && <ErrorNote>{credsError}</ErrorNote>}
              <div className="flex justify-end">
                <Button
                  onClick={() => void handleSaveCreds()}
                  disabled={
                    saveCredsMutation.isPending || !clientId || !clientSecret
                  }
                >
                  <Save />
                  {saveCredsMutation.isPending ? "Saving…" : "Save & continue"}
                </Button>
              </div>
            </div>
          ) : (
            <div className="space-y-3">
              {/* Update-mode guidance. Credentials already exist — either the user
                  saved them, or this is an aliased child inheriting its parent's app
                  — so the wizard opens straight on Connect and the setup steps were
                  never rendered at all. That hid the per-service instruction that
                  matters most for a child ("also enable the Google Calendar API"),
                  and left the user with no statement of WHICH application to edit.
                  The redirect URI block above is deliberately not repeated here. */}
              {provider.setup_mode === "update" &&
                provider.setup_steps.length > 0 && (
                  <div className="space-y-2 rounded-lg border border-border bg-muted-surface p-3">
                    <div className="text-xs font-semibold uppercase tracking-wide text-muted-2">
                      Update your existing{" "}
                      {provider.app_label || provider.label} application
                    </div>
                    <p className="text-sm text-muted-2">
                      Edit the application you already registered — do not create a
                      second one. Confirm the redirect URI above is listed under its
                      authorized redirect URIs.
                    </p>
                    <ol className="space-y-2">
                      {provider.setup_steps.map((s, i) => (
                        <li
                          key={i}
                          className="flex gap-3 rounded-lg border border-border bg-background p-3 text-sm"
                        >
                          <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-muted-surface text-xs font-semibold">
                            {i + 1}
                          </span>
                          <span className="leading-relaxed">
                            <SetupStep
                              text={s}
                              redirectURI={provider.redirect_uri}
                            />
                          </span>
                        </li>
                      ))}
                    </ol>
                    {provider.setup_url && (
                      <a
                        href={provider.setup_url}
                        target="_blank"
                        rel="noreferrer"
                        className="block rounded-md border border-primary/30 bg-primary/5 px-3 py-2 text-sm font-medium text-primary underline underline-offset-2"
                      >
                        {provider.setup_url}
                      </a>
                    )}
                    <div className="flex justify-end">
                      {/* Without this there is no route back to the credentials form
                          once has_creds flips true, so a wrong client secret could
                          not be corrected from this screen at all. */}
                      <Button
                        variant="ghost"
                        onClick={() => setView("creds")}
                      >
                        <Save />
                        Re-enter credentials
                      </Button>
                    </div>
                  </div>
                )}
              {/* connect_inputs on the OAuth path: values that cannot be discovered
                  from any API (a Google Ads developer token) and so must be collected
                  BEFORE consent — they ride the signed state through the provider. */}
              {provider.connect_inputs.map((ci) => (
                <div key={ci.key} className="space-y-1">
                  <Label htmlFor={`svc-oauth-input-${ci.key}`}>
                    {ci.label}
                    {ci.required && <span className="text-danger"> *</span>}
                  </Label>
                  <Input
                    id={`svc-oauth-input-${ci.key}`}
                    value={inputs[ci.key] ?? ""}
                    onChange={(e) =>
                      setInputs((v) => ({ ...v, [ci.key]: e.target.value }))
                    }
                    autoComplete="off"
                  />
                  {ci.hint && <p className="text-xs text-muted-2">{ci.hint}</p>}
                </div>
              ))}
              <div className="space-y-1">
                <Label htmlFor="svc-label">Label (optional)</Label>
                <Input
                  id="svc-label"
                  placeholder="e.g. work, personal"
                  value={label}
                  onChange={(e) => setLabel(e.target.value)}
                />
              </div>
              {connectError && <ErrorNote>{connectError}</ErrorNote>}
              <div className="flex items-center justify-between">
                <button
                  type="button"
                  className="text-xs text-muted-2 underline underline-offset-2"
                  onClick={() => setView("creds")}
                >
                  edit app credentials
                </button>
                <Button
                  onClick={() => void handleConnect()}
                  disabled={connectServiceMutation.isPending || hardBlocked}
                >
                  <Link2 />
                  {connectServiceMutation.isPending
                    ? "Connecting…"
                    : `Connect ${provider.label}`}
                </Button>
              </div>
            </div>
          )
        ) : (
          <div className="space-y-3">
            <div className="space-y-1">
              <Label htmlFor="svc-api-key">
                {provider.key_label || `${provider.label} API key`}
              </Label>
              <Input
                id="svc-api-key"
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                autoComplete="new-password"
              />
              {provider.key_hint && (
                <p className="text-xs text-muted-2">{provider.key_hint}</p>
              )}
            </div>
            {provider.connect_inputs.map((ci) => (
              <div key={ci.key} className="space-y-1">
                <Label htmlFor={`svc-input-${ci.key}`}>
                  {ci.label}
                  {ci.required && <span className="text-danger"> *</span>}
                </Label>
                <Input
                  id={`svc-input-${ci.key}`}
                  value={inputs[ci.key] ?? ""}
                  onChange={(e) =>
                    setInputs((v) => ({ ...v, [ci.key]: e.target.value }))
                  }
                  autoComplete="off"
                />
                {ci.hint && <p className="text-xs text-muted-2">{ci.hint}</p>}
              </div>
            ))}
            <div className="space-y-1">
              <Label htmlFor="svc-key-label">Label (optional)</Label>
              <Input
                id="svc-key-label"
                placeholder="e.g. work, personal"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
              />
            </div>
            {keyError && <ErrorNote>{keyError}</ErrorNote>}
            <div className="flex justify-end">
              <Button
                onClick={() => void handleConnectAPIKey()}
                disabled={connectAPIKeyMutation.isPending || !apiKey}
              >
                <Link2 />
                {connectAPIKeyMutation.isPending ? "Connecting…" : "Connect"}
              </Button>
            </div>
          </div>
        )}
      </div>
    </PanelBody>
  );
}

export default ServiceWizard;
