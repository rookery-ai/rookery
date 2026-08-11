import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ApiError } from "@/lib/api";
import {
  useCreateMCPServer,
  useSyncMCPServer,
  useUpdateMCPServer,
  type MCPServer,
} from "@/lib/mcp";

type AuthKind = "none" | "bearer" | "header";

// MCPServerWizard adds or edits one server.
//
// Saving immediately runs a real sync rather than only writing the row. That is the
// point of the flow: the tool list a server returns IS the review step, and a server
// that cannot complete initialize + tools/list should be visible as broken now
// rather than at 03:00 during a scheduled run. Real-world MCP server conformance
// varies far more than our own code does.
export function MCPServerWizard({
  server,
  onClose,
}: {
  server: MCPServer | null;
  onClose: () => void;
}) {
  const editing = !!server;
  const [name, setName] = useState(server?.name ?? "");
  const [url, setUrl] = useState(server?.url ?? "");
  const [authKind, setAuthKind] = useState<AuthKind>(server?.auth_kind ?? "none");
  const [headerName, setHeaderName] = useState(server?.header_name ?? "");
  const [token, setToken] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const create = useCreateMCPServer();
  const update = useUpdateMCPServer();
  const sync = useSyncMCPServer();
  const busy = create.isPending || update.isPending || sync.isPending;

  async function save() {
    setError(null);
    setNote(null);
    const input = {
      name,
      url,
      auth_kind: authKind,
      header_name: authKind === "header" ? headerName : "",
      token,
    };
    try {
      const id = editing
        ? (await update.mutateAsync({ id: server!.id, ...input })).server.id
        : (await create.mutateAsync(input)).server.id;

      const r = await sync.mutateAsync(id);
      if (r.error) {
        // The row is saved either way — the owner can fix the URL or token and
        // retry without retyping everything. Only the sync failed.
        setError(r.error);
        return;
      }
      onClose();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{editing ? "Edit MCP server" : "Add MCP server"}</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <label className="block">
            <span className="mb-1 block text-sm font-medium">Name</span>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Home Assistant"
            />
          </label>

          <label className="block">
            <span className="mb-1 block text-sm font-medium">Server URL</span>
            <Input
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="http://192.168.1.10:3000/mcp"
            />
            <span className="mt-1 block text-xs text-muted-2">
              A server on your own network is fine — Rookery reaches private
              addresses on purpose.
            </span>
          </label>

          <label className="block">
            <span className="mb-1 block text-sm font-medium">Authentication</span>
            <select
              value={authKind}
              onChange={(e) => setAuthKind(e.target.value as AuthKind)}
              className="h-9 w-full rounded-md border border-chrome bg-background px-3 text-sm"
            >
              <option value="none">None</option>
              <option value="bearer">Bearer token</option>
              <option value="header">Custom header</option>
            </select>
          </label>

          {authKind === "header" && (
            <label className="block">
              <span className="mb-1 block text-sm font-medium">Header name</span>
              <Input
                value={headerName}
                onChange={(e) => setHeaderName(e.target.value)}
                placeholder="X-Api-Key"
              />
            </label>
          )}

          {authKind !== "none" && (
            <label className="block">
              <span className="mb-1 block text-sm font-medium">
                {editing && server?.has_token ? "Replace token" : "Token"}
              </span>
              <Input
                type="password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder={
                  editing && server?.has_token
                    ? "Leave blank to keep the stored token"
                    : ""
                }
              />
            </label>
          )}

          <p className="text-xs text-muted-2">
            Saving asks the server what tools it has. On a new server they start
            switched on so you can use it straight away — anything that appears
            later stays off until you enable it.
          </p>

          {error && (
            <p className="rounded-md bg-danger-soft px-3 py-2 text-sm text-danger">
              {error}
            </p>
          )}
          {note && <p className="text-sm text-muted-2">{note}</p>}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={busy || !name || !url}>
            {busy ? "Checking the server…" : editing ? "Save & re-sync" : "Test & save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
