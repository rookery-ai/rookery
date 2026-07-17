import { useEffect, useMemo, useRef, useState } from "react";
import { Search } from "lucide-react";
import { ContextPane, useSlideOver } from "@/components/shell/AppShell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { ProviderLogo } from "@/components/brand/ProviderLogo";
import {
  useConnectors,
  useServices,
  type ConnectorPlatform,
  type ServiceProvider,
} from "@/lib/connections";

const SEARCH_DEBOUNCE_MS = 150;

function matches(query: string, ...fields: string[]) {
  if (!query) return true;
  return fields.some((f) => f.toLowerCase().includes(query));
}

function errorMessage(error: unknown) {
  return error instanceof ApiError || error instanceof Error
    ? error.message
    : "Something went wrong";
}

function ErrorBanner({ message }: { message: string }) {
  return (
    <div className="mb-3 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">{message}</div>
  );
}

function LoadingNote({ text }: { text: string }) {
  return <div className="p-4 text-sm text-muted-2">{text}</div>;
}

function EmptyNote({ text }: { text: string }) {
  return <div className="p-4 text-sm text-muted-2">{text}</div>;
}

// ── Chat apps ────────────────────────────────────────────────────────────

function ChatAppCard({
  platform,
  onOpen,
}: {
  platform: ConnectorPlatform;
  onOpen: (platform: ConnectorPlatform) => void;
}) {
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border bg-background p-4">
      <div className="flex items-center gap-3">
        <ProviderLogo name={platform.platform} size={34} />
        <div className="min-w-0">
          <div className="truncate font-semibold">{platform.label}</div>
          {platform.connected ? (
            <div className="flex items-center gap-1 text-xs text-ok">
              <span className="size-1.5 rounded-full bg-ok" /> Connected
            </div>
          ) : (
            <div className="text-xs text-muted-2">Not connected</div>
          )}
        </div>
      </div>
      {platform.connected && platform.identity && (
        <div className="truncate text-xs text-muted-2">{platform.identity}</div>
      )}
      <Button
        variant={platform.connected ? "outline" : "default"}
        size="sm"
        onClick={() => onOpen(platform)}
      >
        {platform.connected ? "Manage" : "Connect"}
      </Button>
    </div>
  );
}

// ── Services ─────────────────────────────────────────────────────────────

function ServiceTile({
  provider,
  onOpen,
}: {
  provider: ServiceProvider;
  onOpen: (provider: ServiceProvider) => void;
}) {
  const count = provider.connections.length;
  const needsReauth = provider.connections.some((c) => c.status !== "ACTIVE");

  return (
    <button
      type="button"
      onClick={() => onOpen(provider)}
      className="flex flex-col items-center gap-1.5 rounded-lg border border-border bg-background p-3 text-center transition-colors hover:border-primary/40 hover:shadow-sm"
    >
      <ProviderLogo name={provider.name} size={30} />
      <div className="w-full truncate text-xs font-semibold">{provider.label}</div>
      {count === 0 ? (
        <div className="text-[11px] text-muted-2">Connect</div>
      ) : needsReauth ? (
        <div className="text-[11px] text-warn">reconnect needed</div>
      ) : (
        <div className="text-[11px] text-ok">
          ● {count} account{count > 1 ? "s" : ""}
        </div>
      )}
    </button>
  );
}

export default function ConnectionsPage() {
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");
  const { open } = useSlideOver();

  useEffect(() => {
    const t = window.setTimeout(() => setQuery(input), SEARCH_DEBOUNCE_MS);
    return () => window.clearTimeout(t);
  }, [input]);

  const q = query.trim().toLowerCase();

  const connectorsQuery = useConnectors();
  const servicesQuery = useServices();

  const platforms = connectorsQuery.data?.platforms ?? [];
  const services = servicesQuery.data?.providers ?? [];

  const filteredPlatforms = useMemo(
    () => platforms.filter((p) => matches(q, p.platform, p.label)),
    [platforms, q],
  );
  const filteredServices = useMemo(
    () => services.filter((p) => matches(q, p.name, p.label)),
    [services, q],
  );

  const connectedServicesCount = useMemo(
    () => services.filter((p) => p.connections.length > 0).length,
    [services],
  );

  const chatAppsRef = useRef<HTMLDivElement>(null);
  const servicesRef = useRef<HTMLDivElement>(null);

  function scrollTo(ref: React.RefObject<HTMLDivElement | null>) {
    ref.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  }

  function openChatWizard(platform: ConnectorPlatform) {
    open(<div>wizard: {platform.platform}</div>, {
      title: `${platform.connected ? "Manage" : "Connect"} ${platform.label}`,
    });
  }

  function openServiceWizard(provider: ServiceProvider) {
    open(<div>wizard: {provider.name}</div>, {
      title: `Connect ${provider.label}`,
    });
  }

  return (
    <>
      <ContextPane>
        <div className="flex h-full flex-col gap-3 p-3">
          <h2 className="px-1 text-sm font-bold">Connections</h2>
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-2" />
            <Input
              aria-label="Search providers"
              placeholder="Search providers…"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              className="h-8 pl-8 text-sm"
            />
          </div>
          <div className="flex flex-col gap-1">
            <button
              type="button"
              onClick={() => scrollTo(chatAppsRef)}
              className="flex items-center justify-between rounded-md px-2.5 py-1.5 text-sm font-medium hover:bg-chrome"
            >
              <span>💬 Chat apps</span>
              <span className="text-muted-2">{platforms.length}</span>
            </button>
            <button
              type="button"
              onClick={() => scrollTo(servicesRef)}
              className="flex items-center justify-between rounded-md px-2.5 py-1.5 text-sm font-medium hover:bg-chrome"
            >
              <span>🧩 Services</span>
              <span className="text-muted-2">
                {connectedServicesCount} of {services.length}
              </span>
            </button>
          </div>
          <div className="rounded-lg bg-muted p-3 text-xs leading-relaxed text-muted-2">
            <p>
              <b className="text-foreground">Chat apps</b> are where you talk to your assistant.
            </p>
            <p className="mt-2">
              <b className="text-foreground">Services</b> are the accounts your agents can act on
              — Gmail, Notion, GitHub…
            </p>
          </div>
        </div>
      </ContextPane>

      <div className="mx-auto max-w-5xl p-6">
        <section ref={chatAppsRef} className="mb-10 scroll-mt-4">
          <h2 className="text-lg font-bold">Chat apps</h2>
          <p className="mb-4 text-sm text-muted-2">
            Talk to your workspace from the messenger you already use.
          </p>
          {connectorsQuery.isError && <ErrorBanner message={errorMessage(connectorsQuery.error)} />}
          {connectorsQuery.isLoading && <LoadingNote text="Loading chat apps…" />}
          {!connectorsQuery.isLoading &&
            !connectorsQuery.isError &&
            filteredPlatforms.length === 0 && (
              <EmptyNote
                text={q ? `No chat apps match “${query.trim()}”.` : "No chat apps available."}
              />
            )}
          {!connectorsQuery.isLoading && !connectorsQuery.isError && filteredPlatforms.length > 0 && (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {filteredPlatforms.map((p) => (
                <ChatAppCard key={p.platform} platform={p} onOpen={openChatWizard} />
              ))}
            </div>
          )}
        </section>

        <section ref={servicesRef} className="scroll-mt-4">
          <h2 className="text-lg font-bold">Services</h2>
          <p className="mb-4 text-sm text-muted-2">
            Give your agents superpowers — connect the tools they'll work with.
          </p>
          {servicesQuery.isError && <ErrorBanner message={errorMessage(servicesQuery.error)} />}
          {servicesQuery.isLoading && <LoadingNote text="Loading services…" />}
          {!servicesQuery.isLoading && !servicesQuery.isError && filteredServices.length === 0 && (
            <EmptyNote
              text={q ? `No services match “${query.trim()}”.` : "No services available."}
            />
          )}
          {!servicesQuery.isLoading && !servicesQuery.isError && filteredServices.length > 0 && (
            <div className={cn("grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4")}>
              {filteredServices.map((p) => (
                <ServiceTile key={p.name} provider={p} onOpen={openServiceWizard} />
              ))}
            </div>
          )}
        </section>
      </div>
    </>
  );
}
