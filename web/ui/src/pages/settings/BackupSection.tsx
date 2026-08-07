import { useEffect, useState } from "react";
import { AlertTriangle, Download, HardDriveDownload, HardDriveUpload, Save, ShieldCheck, Trash2 } from "lucide-react";
import { OwnerIcon } from "./OwnerSections";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { timeAgo } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import {
  formatBytes,
  useBackupConfig,
  useDeleteSnapshot,
  useRestoreSnapshot,
  useRunBackup,
  useSaveBackupConfig,
  useSnapshots,
  useVerifySnapshot,
  type BackupConfig,
  type SaveBackupConfig,
} from "@/lib/backup";

function errMsg(err: unknown) {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

function ErrorNote({ message }: { message: string }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-sm text-danger">
      <AlertTriangle className="size-3.5 shrink-0" />
      {message}
    </div>
  );
}

const WEEKDAYS = [
  "Sunday",
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
];

// Form state is kept separate from the fetched config because the passphrase
// and S3 secret are write-only — the server never sends them back, so they
// cannot be round-tripped through the query cache.
type FormState = {
  enabled: boolean;
  destination: "local" | "s3";
  schedule: "daily" | "weekly";
  hour: number;
  weekday: number;
  retention: number;
  passphrase: string;
  localDir: string;
  s3Endpoint: string;
  s3Region: string;
  s3Bucket: string;
  s3Prefix: string;
  s3AccessKey: string;
  s3SecretKey: string;
  s3PathStyle: boolean;
};

export function BackupSection() {
  const { data, isLoading, isError, error } = useBackupConfig();
  const save = useSaveBackupConfig();
  const run = useRunBackup();
  const del = useDeleteSnapshot();
  const verify = useVerifySnapshot();
  const restore = useRestoreSnapshot();

  // Gate on the passphrase, not the enabled toggle: an owner who configured a
  // destination but left automatic runs off can still use "Back up now", and
  // hiding their snapshots would make those backups look lost.
  const configured = Boolean(data?.passphrase_set);
  const snapshots = useSnapshots(configured);

  const [form, setForm] = useState<FormState | null>(null);
  const [changingPassphrase, setChangingPassphrase] = useState(false);
  const [restoreTarget, setRestoreTarget] = useState<string | null>(null);
  const [restorePassphrase, setRestorePassphrase] = useState("");
  const [restoreConfirm, setRestoreConfirm] = useState("");
  const [notice, setNotice] = useState("");

  useEffect(() => {
    if (!data || form) return;
    // Defensive defaults: this section is one of several on the settings page,
    // and a malformed payload here must not blank the whole page.
    const s3 = data.s3 ?? ({} as BackupConfig["s3"]);
    setForm({
      enabled: Boolean(data.enabled),
      destination: data.destination ?? "local",
      schedule: data.schedule ?? "daily",
      hour: data.hour ?? 3,
      weekday: data.weekday ?? 0,
      retention: data.retention ?? 7,
      passphrase: "",
      localDir: data.local_dir ?? "",
      s3Endpoint: s3.endpoint ?? "",
      s3Region: s3.region ?? "",
      s3Bucket: s3.bucket ?? "",
      s3Prefix: s3.prefix ?? "",
      s3AccessKey: s3.access_key ?? "",
      s3SecretKey: "",
      s3PathStyle: Boolean(s3.path_style),
    });
  }, [data, form]);

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) =>
    setForm((f) => (f ? { ...f, [key]: value } : f));

  function onSave() {
    if (!form) return;
    const body: SaveBackupConfig = {
      enabled: form.enabled,
      destination: form.destination,
      schedule: form.schedule,
      hour: form.hour,
      weekday: form.weekday,
      retention: form.retention,
      local: { dir: form.localDir },
      s3: {
        endpoint: form.s3Endpoint,
        region: form.s3Region,
        bucket: form.s3Bucket,
        prefix: form.s3Prefix,
        access_key: form.s3AccessKey,
        path_style: form.s3PathStyle,
      },
    };
    // An omitted passphrase means "keep the stored one" — sending an empty
    // string would be indistinguishable from clearing it.
    if (form.passphrase) body.passphrase = form.passphrase;
    if (form.s3SecretKey) body.s3.secret_key = form.s3SecretKey;

    save.mutate(body, {
      onSuccess: () => {
        setNotice("Backup settings saved.");
        setChangingPassphrase(false);
        setForm((f) => (f ? { ...f, passphrase: "", s3SecretKey: "" } : f));
      },
    });
  }

  // These three states are deliberately NOT collapsed into one branch. They
  // were, and the result was that a request answered by the SPA catch-all
  // (200 index.html → parses to null → no error object) rendered a bare
  // "Something went wrong" with nothing to act on, while the server log showed
  // a 200. An error state that cannot say what failed costs more than it saves.
  if (isLoading || (!data && !isError)) {
    return (
      <div>
        <div className="flex items-center gap-2.5">
        <OwnerIcon slug="owner-backup" />
        <h2 className="text-lg font-bold">Backup</h2>
      </div>
        <p className="mt-2 text-sm text-muted-2">Loading…</p>
      </div>
    );
  }
  if (isError || !data) {
    return (
      <div>
        <div className="flex items-center gap-2.5">
        <OwnerIcon slug="owner-backup" />
        <h2 className="text-lg font-bold">Backup</h2>
      </div>
        <div className="mt-2">
          <ErrorNote
            message={
              isError
                ? errMsg(error)
                : "The server returned no backup settings. Check that /api/v1/backup/config is reachable."
            }
          />
        </div>
      </div>
    );
  }
  // data has arrived but the form-state effect has not run yet: one render, and
  // it is a loading state, not a failure.
  if (!form) {
    return (
      <div>
        <div className="flex items-center gap-2.5">
        <OwnerIcon slug="owner-backup" />
        <h2 className="text-lg font-bold">Backup</h2>
      </div>
        <p className="mt-2 text-sm text-muted-2">Loading…</p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center gap-2.5">
        <OwnerIcon slug="owner-backup" />
        <h2 className="text-lg font-bold">Backup</h2>
      </div>
      <p className="mt-1 text-sm text-muted-2">
        A snapshot covers every workspace — knowledge base, agents, skills,
        secrets and settings — in one encrypted file. Times are in the server's
        local timezone.
      </p>

      {data.pending_restore && (
        <div className="mt-3 rounded-md bg-warn-soft px-3 py-2 text-sm text-warn">
          A restore is staged and will be applied the next time the server
          starts. Run <code>rookery backup cancel-restore</code> to abandon it.
        </div>
      )}

      {/* Status */}
      <dl className="mt-3 flex flex-wrap gap-x-8 gap-y-2 text-sm">
        <div>
          <dt className="text-muted-2">Last run</dt>
          <dd
            className={
              data.last_status === "error"
                ? "font-medium text-danger"
                : "font-medium"
            }
          >
            {data.last_run_at && !data.last_run_at.startsWith("0001")
              ? timeAgo(data.last_run_at)
              : "never"}
          </dd>
        </div>
        <div>
          <dt className="text-muted-2">Next run</dt>
          <dd className="font-medium">
            {data.enabled &&
            data.next_run_at &&
            !data.next_run_at.startsWith("0001")
              ? new Date(data.next_run_at).toLocaleString()
              : "not scheduled"}
          </dd>
        </div>
        <div>
          <dt className="text-muted-2">Last size</dt>
          <dd className="font-medium">{formatBytes(data.last_size)}</dd>
        </div>
      </dl>

      {data.last_status === "error" && data.last_error && (
        <div className="mt-2">
          <ErrorNote message={data.last_error} />
        </div>
      )}

      {/* Settings form */}
      <div className="mt-4 space-y-3">
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) => set("enabled", e.target.checked)}
          />
          <span className="font-medium">Run backups automatically</span>
        </label>

        <div className="flex flex-wrap gap-3">
          <label className="text-sm">
            <span className="block text-muted-2">Destination</span>
            <select
              className="mt-1 rounded-md border border-border bg-background px-2 py-1 text-sm outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
              value={form.destination}
              onChange={(e) =>
                set("destination", e.target.value as "local" | "s3")
              }
            >
              <option value="local">Local folder</option>
              <option value="s3">S3-compatible</option>
            </select>
          </label>

          <label className="text-sm">
            <span className="block text-muted-2">Frequency</span>
            <select
              className="mt-1 rounded-md border border-border bg-background px-2 py-1 text-sm outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
              value={form.schedule}
              onChange={(e) =>
                set("schedule", e.target.value as "daily" | "weekly")
              }
            >
              <option value="daily">Daily</option>
              <option value="weekly">Weekly</option>
            </select>
          </label>

          {form.schedule === "weekly" && (
            <label className="text-sm">
              <span className="block text-muted-2">Day</span>
              <select
                className="mt-1 rounded-md border border-border bg-background px-2 py-1 text-sm outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
                value={form.weekday}
                onChange={(e) => set("weekday", Number(e.target.value))}
              >
                {WEEKDAYS.map((d, i) => (
                  <option key={d} value={i}>
                    {d}
                  </option>
                ))}
              </select>
            </label>
          )}

          <label className="text-sm">
            <span className="block text-muted-2">Hour (server local time)</span>
            <select
              className="mt-1 rounded-md border border-border bg-background px-2 py-1 text-sm outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
              value={form.hour}
              onChange={(e) => set("hour", Number(e.target.value))}
            >
              {Array.from({ length: 24 }, (_, h) => (
                <option key={h} value={h}>
                  {String(h).padStart(2, "0")}:00
                </option>
              ))}
            </select>
          </label>

          <label className="text-sm">
            <span className="block text-muted-2">Keep last</span>
            <Input
              type="number"
              min={1}
              className="mt-1 w-20"
              value={form.retention}
              onChange={(e) => set("retention", Number(e.target.value))}
            />
          </label>
        </div>

        {form.destination === "local" ? (
          <label className="block text-sm">
            <span className="block text-muted-2">Backup folder</span>
            <Input
              className="mt-1"
              placeholder="/mnt/backups"
              value={form.localDir}
              onChange={(e) => set("localDir", e.target.value)}
            />
          </label>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="text-sm">
              <span className="block text-muted-2">Bucket</span>
              <Input
                className="mt-1"
                value={form.s3Bucket}
                onChange={(e) => set("s3Bucket", e.target.value)}
              />
            </label>
            <label className="text-sm">
              <span className="block text-muted-2">Region</span>
              <Input
                className="mt-1"
                value={form.s3Region}
                onChange={(e) => set("s3Region", e.target.value)}
              />
            </label>
            <label className="text-sm">
              <span className="block text-muted-2">
                Endpoint (blank for AWS)
              </span>
              <Input
                className="mt-1"
                placeholder="https://s3.us-west-002.backblazeb2.com"
                value={form.s3Endpoint}
                onChange={(e) => set("s3Endpoint", e.target.value)}
              />
            </label>
            <label className="text-sm">
              <span className="block text-muted-2">Prefix</span>
              <Input
                className="mt-1"
                placeholder="rookery/"
                value={form.s3Prefix}
                onChange={(e) => set("s3Prefix", e.target.value)}
              />
            </label>
            <label className="text-sm">
              <span className="block text-muted-2">Access key</span>
              <Input
                className="mt-1"
                value={form.s3AccessKey}
                onChange={(e) => set("s3AccessKey", e.target.value)}
              />
            </label>
            <label className="text-sm">
              <span className="block text-muted-2">
                Secret key{" "}
                {data.s3.secret_key_set && (
                  <span className="text-ok">(set)</span>
                )}
              </span>
              <Input
                className="mt-1"
                type="password"
                placeholder={data.s3.secret_key_set ? "unchanged" : ""}
                value={form.s3SecretKey}
                onChange={(e) => set("s3SecretKey", e.target.value)}
                autoComplete="new-password"
              />
            </label>
            <label className="flex items-center gap-2 text-sm sm:col-span-2">
              <input
                type="checkbox"
                checked={form.s3PathStyle}
                onChange={(e) => set("s3PathStyle", e.target.checked)}
              />
              <span>
                Use path-style URLs (MinIO and some R2 setups need this)
              </span>
            </label>
          </div>
        )}

        {/* Passphrase */}
        <div>
          {data.passphrase_set && !changingPassphrase ? (
            <div className="flex items-center gap-2 text-sm">
              <ShieldCheck className="size-3.5 text-ok" />
              <span>Passphrase is set.</span>
              <Button
                type="button"
                variant="link"
                className="h-auto p-0 text-muted-2"
                onClick={() => setChangingPassphrase(true)}
              >
                Change
              </Button>
            </div>
          ) : (
            <label className="block text-sm">
              <span className="block text-muted-2">Encryption passphrase</span>
              <Input
                className="mt-1"
                type="password"
                value={form.passphrase}
                onChange={(e) => set("passphrase", e.target.value)}
                autoComplete="new-password"
              />
            </label>
          )}
          <p className="mt-1 text-sm text-danger">
            Write this down. It is the only way to recover your data — nobody
            can reset it for you.
          </p>
          {data.passphrase_set && (
            <p className="mt-1 text-sm text-muted-2">
              Changing it does not re-encrypt existing snapshots; each stays
              readable with the passphrase in force when it was written.
            </p>
          )}
        </div>

        {save.isError && <ErrorNote message={errMsg(save.error)} />}
        {notice && <p className="text-sm text-ok">{notice}</p>}

        <div className="flex gap-2">
          <Button onClick={onSave} disabled={save.isPending}>
            <Save />
            {save.isPending ? "Saving…" : "Save"}
          </Button>
          <Button
            variant="secondary"
            onClick={() =>
              run.mutate(undefined, {
                onSuccess: (r) => setNotice(`Wrote ${r.name}.`),
              })
            }
            disabled={run.isPending || !data.passphrase_set}
          >
            <HardDriveDownload />
            {run.isPending ? "Backing up…" : "Back up now"}
          </Button>
        </div>
        {run.isError && <ErrorNote message={errMsg(run.error)} />}
      </div>

      {/* Snapshots */}
      {configured && (
        <div className="mt-5">
          <h4 className="text-xs font-bold tracking-wide text-muted-2 uppercase">Snapshots</h4>
          {snapshots.isError && (
            <div className="mt-2">
              <ErrorNote message={errMsg(snapshots.error)} />
            </div>
          )}
          {snapshots.data?.length === 0 && (
            <p className="mt-2 text-sm text-muted-2">No snapshots yet.</p>
          )}
          <ul className="mt-2 divide-y divide-border text-sm">
            {snapshots.data?.map((s) => (
              <li
                key={s.name}
                className="flex flex-wrap items-center gap-2 py-2"
              >
                <span className="font-mono">{s.name}</span>
                <span className="text-muted-2">{formatBytes(s.size)}</span>
                {/* Real Buttons, not underlined text: these are actions, and
                    one of them opens a destructive restore. As raw elements
                    they also missed the variant's own icon sizing, which is
                    why the icons were hand-set to size-3.5. */}
                <span className="ml-auto flex items-center gap-2">
                  <Button asChild variant="outline" size="sm">
                    <a
                      href={`/api/v1/backup/snapshots/${encodeURIComponent(s.name)}/download`}
                    >
                      <Download />
                      Download
                    </a>
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setRestoreTarget(s.name)}
                  >
                    <HardDriveUpload />
                    Restore from snapshot
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="text-danger"
                    onClick={() => del.mutate(s.name)}
                  >
                    <Trash2 />
                    Delete
                  </Button>
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Restore dialog */}
      {restoreTarget && (
        <div className="mt-4 rounded-md border border-danger p-3">
          <p className="text-sm font-medium text-danger">
            Restore {restoreTarget}? This replaces the database and every
            workspace vault.
          </p>
          <p className="mt-1 text-sm text-muted-2">
            The current data is moved aside into a <code>.pre-restore-*</code>{" "}
            folder first.
          </p>
          <div className="mt-2 grid gap-2 sm:grid-cols-2">
            <label className="text-sm">
              <span className="block text-muted-2">Passphrase</span>
              <Input
                className="mt-1"
                type="password"
                value={restorePassphrase}
                onChange={(e) => setRestorePassphrase(e.target.value)}
                autoComplete="new-password"
              />
            </label>
            <label className="text-sm">
              <span className="block text-muted-2">
                Type RESTORE to confirm
              </span>
              <Input
                className="mt-1"
                aria-label="Type RESTORE to confirm"
                value={restoreConfirm}
                onChange={(e) => setRestoreConfirm(e.target.value)}
              />
            </label>
          </div>
          {restore.isError && (
            <div className="mt-2">
              <ErrorNote message={errMsg(restore.error)} />
            </div>
          )}
          {restore.data && (
            <p className="mt-2 text-sm text-warn">{restore.data.message}</p>
          )}
          <div className="mt-2 flex gap-2">
            <Button
              variant="destructive"
              disabled={restoreConfirm !== "RESTORE" || restore.isPending}
              onClick={() =>
                restore.mutate({
                  name: restoreTarget,
                  passphrase: restorePassphrase,
                  confirm: restoreConfirm,
                })
              }
            >
            <HardDriveUpload />
              {restore.isPending ? "Staging…" : "Restore"}
            </Button>
            <Button
              variant="secondary"
              onClick={() =>
                verify.mutate(
                  { name: restoreTarget, passphrase: restorePassphrase },
                  {
                    onSuccess: (r) =>
                      setNotice(`Snapshot is intact: ${r.files} files.`),
                  },
                )
              }
              disabled={verify.isPending}
            >
            <ShieldCheck />
              {verify.isPending ? "Verifying…" : "Verify only"}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setRestoreTarget(null);
                setRestoreConfirm("");
                setRestorePassphrase("");
              }}
            >
              Cancel
            </Button>
          </div>
          {verify.isError && (
            <div className="mt-2">
              <ErrorNote message={errMsg(verify.error)} />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
