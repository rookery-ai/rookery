import { useState } from "react";
import type { ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { useSlideOver } from "@/components/shell/AppShell";
import { PanelBody } from "@/components/shell/PanelBody";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError } from "@/lib/api";
import { Linkify } from "@/lib/linkify";
import {
  useServices,
  useSaveProviderCreds,
  useConnectService,
  useConnectAPIKey,
  useDeleteServiceConnection,
  type ServiceProvider,
  type ServiceConnection,
} from "@/lib/connections";

type ServiceWizardProps = { provider: ServiceProvider };

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
    <div className="space-y-2 rounded-lg border border-border p-3 text-sm">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate font-medium">{connection.label}</div>
          {connection.identity && (
            <div className="truncate text-xs text-muted-2">{connection.identity}</div>
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
            Reconnect
          </Button>
        )}
        {!confirming ? (
          <Button size="sm" variant="outline" className="text-danger" onClick={() => setConfirming(true)}>
            Disconnect
          </Button>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs text-danger">Disconnect {connection.label}?</span>
            <Button size="sm" variant="outline" onClick={() => setConfirming(false)}>
              Cancel
            </Button>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => void handleDisconnect()}
              disabled={deleteMutation.isPending}
            >
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

export function ServiceWizard({ provider: initialProvider }: ServiceWizardProps) {
  const { close } = useSlideOver();

  // Prefer live data — after a connect/disconnect/reconnect mutation
  // invalidates the ["services"] query, this refetches and the connected
  // accounts list updates in place without closing the panel (a provider can
  // have many accounts, unlike a chat-app platform's single connection).
  // Fall back to the snapshot the caller opened the panel with while that
  // first fetch is in flight, or if the provider ever drops out of the list.
  const servicesQuery = useServices();
  const provider =
    servicesQuery.data?.providers.find((p) => p.name === initialProvider.name) ?? initialProvider;

  const [view, setView] = useState<"creds" | "connect">(provider.has_creds ? "connect" : "creds");
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

  function jumpToConnect(seedLabel: string) {
    setView("connect");
    setLabel(seedLabel);
  }

  async function handleSaveCreds() {
    setCredsError(null);
    try {
      await saveCredsMutation.mutateAsync({ provider: provider.name, clientId, clientSecret });
      setView("connect");
    } catch (err) {
      setCredsError(errMsg(err));
    }
  }

  async function handleConnect() {
    setConnectError(null);
    try {
      const res = await connectServiceMutation.mutateAsync({ provider: provider.name, label });
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

  return (
    <PanelBody>
      {hasConnections && (
        <div className="space-y-2">
          <div className="text-xs font-semibold uppercase tracking-wide text-muted-2">
            Connected accounts
          </div>
          {provider.connections.map((c) => (
            <AccountRow key={c.id} connection={c} onReconnect={() => jumpToConnect(c.label)} />
          ))}
        </div>
      )}

      <div className={hasConnections ? "space-y-3 border-t border-border pt-4" : "space-y-3"}>
        {provider.kind === "oauth" ? (
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
                      <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-muted text-[11px] font-semibold">
                        {i + 1}
                      </span>
                      <span className="leading-relaxed">
                        <Linkify text={s} />
                      </span>
                    </li>
                  ))}
                </ol>
              )}
              <div className="space-y-1">
                <Label htmlFor="svc-client-id">Client ID</Label>
                <Input
                  id="svc-client-id"
                  value={clientId}
                  onChange={(e) => setClientId(e.target.value)}
                  autoComplete="off"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="svc-client-secret">Client secret</Label>
                <Input
                  id="svc-client-secret"
                  type="password"
                  value={clientSecret}
                  onChange={(e) => setClientSecret(e.target.value)}
                  autoComplete="off"
                />
              </div>
              {credsError && <ErrorNote>{credsError}</ErrorNote>}
              <div className="flex justify-end">
                <Button
                  onClick={() => void handleSaveCreds()}
                  disabled={saveCredsMutation.isPending || !clientId || !clientSecret}
                >
                  {saveCredsMutation.isPending ? "Saving…" : "Save & continue"}
                </Button>
              </div>
            </div>
          ) : (
            <div className="space-y-3">
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
                <Button onClick={() => void handleConnect()} disabled={connectServiceMutation.isPending}>
                  {connectServiceMutation.isPending ? "Connecting…" : `Connect ${provider.label} →`}
                </Button>
              </div>
            </div>
          )
        ) : (
          <div className="space-y-3">
            <div className="space-y-1">
              <Label htmlFor="svc-api-key">{provider.label} API key</Label>
              <Input
                id="svc-api-key"
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                autoComplete="off"
              />
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
                  onChange={(e) => setInputs((v) => ({ ...v, [ci.key]: e.target.value }))}
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
