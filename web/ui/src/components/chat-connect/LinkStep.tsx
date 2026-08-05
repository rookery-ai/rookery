import { useEffect, useRef, useState } from "react";
import { ArrowRight, Link2, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { ConnectorPlatform } from "@/lib/connections";
import { OkNote, Spinner, WarningNote } from "./notes";
import { ESCALATE_MS, POLL_LIMIT_MS, type PlatformSource } from "./source";

/**
 * Step 4 of connecting a chat app: the operator sends /start and the bot links
 * their account.
 *
 * The identity row is created only when that /start actually reaches the bot,
 * so its presence proves the inbound path end to end — which a token check
 * cannot. Until it lands there is deliberately no Done button and no green
 * state: the product must never signal completion it has not verified.
 *
 * `source` is injected rather than imported because this component is mounted
 * by two hosts with different reachable transports (see PlatformSource).
 */
export function LinkStep({
  platform,
  source,
  onFinishLater,
  onDone,
}: {
  platform: ConnectorPlatform;
  source: PlatformSource;
  onFinishLater: () => void;
  onDone: () => void;
}) {
  // `linked` starts from the snapshot the step opened with and latches true the
  // moment a poll confirms it — so polling can actually stop once linked,
  // rather than running for the rest of the panel's life.
  const [linked, setLinked] = useState(platform.linked);
  const [elapsed, setElapsed] = useState(0);
  // Bumping this restarts the elapsed clock (and therefore polling) after the
  // wait is abandoned, without remounting the step.
  const [attempt, setAttempt] = useState(0);

  const expired = elapsed >= POLL_LIMIT_MS;
  const live = source.usePlatform(platform.platform, {
    poll: !linked && !expired,
  }) ?? platform;

  useEffect(() => {
    if (live.linked && !linked) setLinked(true);
  }, [live.linked, linked]);

  // One interval per wait. Cleared once linked so a completed step stops
  // scheduling work.
  const startedAt = useRef(0);
  useEffect(() => {
    if (linked) return;
    startedAt.current = Date.now();
    setElapsed(0);
    const id = setInterval(() => {
      setElapsed(Date.now() - startedAt.current);
    }, 1000);
    return () => clearInterval(id);
  }, [linked, attempt]);

  if (live.linked) {
    return (
      <div className="space-y-3">
        <OkNote>Linked as {live.linked_identity}</OkNote>
        <div className="flex justify-end">
          <Button onClick={onDone}>Done</Button>
        </div>
      </div>
    );
  }

  // A saved connection whose server is down is otherwise indistinguishable
  // from one merely waiting for the operator — both rendered the same spinner,
  // which is how a crashed process was read as a broken Discord app.
  const offline = live.bot_online === false;

  return (
    <div className="space-y-3">
      {offline && (
        <WarningNote>
          The {live.label} bot isn't running, so it can't receive your message.
          Start the server, then send <code>/start</code> again.
        </WarningNote>
      )}

      {live.invite_url && (
        <div className="space-y-2 rounded-lg border border-border bg-background p-3 text-sm">
          <p className="font-medium">First, add the bot to a server</p>
          <p className="text-muted-2">
            Discord only allows a direct message between accounts that share a
            server. Afterwards, check the server's Privacy Settings and make sure
            Direct Messages are allowed.
          </p>
          <Button asChild variant="outline" size="sm">
            <a href={live.invite_url} target="_blank" rel="noreferrer">
              <Link2 />
              Invite to a server
            </a>
          </Button>
        </div>
      )}

      <div className="space-y-2 rounded-lg border border-border bg-background p-3 text-sm">
        <p className="font-medium">Then send the bot a direct message</p>
        <p className="text-muted-2">
          Open a direct message with{" "}
          <b className="text-foreground">{live.identity || live.label}</b> and
          send:
        </p>
        <code className="block rounded bg-muted-surface px-2 py-1 font-mono">
          /start
        </code>
        {live.dm_url && (
          <Button asChild variant="outline" size="sm">
            <a href={live.dm_url} target="_blank" rel="noreferrer">
              <ArrowRight />
              Open {live.label}
            </a>
          </Button>
        )}
      </div>

      {!expired && <Spinner text="Waiting for you to send /start…" />}

      {/* Escalation: after a while, stop implying the operator simply hasn't
          acted yet and name the things that actually go wrong. */}
      {(elapsed >= ESCALATE_MS || expired) && (
        <div className="space-y-2 rounded-lg border border-warn/30 bg-warn-soft p-3 text-xs text-warn">
          <p className="font-medium">Not working?</p>
          <ul className="list-disc space-y-1 pl-4">
            <li>
              Send <code>/start</code> as a <b>direct message</b> to the bot. A
              message posted in a server channel is ignored.
            </li>
            <li>You and the bot must both be in the same server.</li>
            <li>
              Check the server's Privacy Settings allow direct messages from
              server members.
            </li>
            {offline && <li>The bot isn't running — start the server first.</li>}
          </ul>
        </div>
      )}

      {expired && (
        <Button
          variant="outline"
          size="sm"
          onClick={() => setAttempt((a) => a + 1)}
        >
          <RotateCcw />
          Keep waiting
        </Button>
      )}

      <div className="flex justify-end">
        <Button variant="link" onClick={onFinishLater}>
          Finish later — I'm not linked yet
        </Button>
      </div>
    </div>
  );
}

export default LinkStep;
