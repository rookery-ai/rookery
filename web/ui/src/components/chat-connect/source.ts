import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { ConnectorPlatform, TestConnectorResponse } from "@/lib/connections";
import { useConnectors, useTestConnector } from "@/lib/connections";

/** Poll cadence while waiting for the operator's /start handshake. */
export const POLL_MS = 2000;

/**
 * How long an unlinked wait runs before the step stops implying the user
 * simply hasn't acted yet and starts offering reasons it might be stuck.
 */
export const ESCALATE_MS = 45_000;

/**
 * Upper bound on polling. The step used to poll for the life of the panel,
 * which against a dead server produced an identical spinner forever — the
 * failure that made a crashed process read as a broken Discord app.
 */
export const POLL_LIMIT_MS = 5 * 60_000;

/**
 * How a host feeds live platform status to the shared connect steps.
 *
 * This indirection exists because the two hosts cannot share a transport:
 * every /api/v1/connectors route sits behind requireSetupCompleteAPI, so all
 * of them 403 while the setup wizard is running. Injecting the source — rather
 * than forking the component per host — is what stops the connections page and
 * onboarding drifting apart again, which is exactly how onboarding ended up
 * with no test and no link step at all.
 */
export type PlatformSource = {
  usePlatform: (
    platform: string,
    opts: { poll: boolean },
  ) => ConnectorPlatform | undefined;
  useTest: () => UseMutationResult<TestConnectorResponse, unknown, string>;
};

/** Source for the connections page, where the full connector API is reachable. */
export const connectorsSource: PlatformSource = {
  usePlatform: (platform, { poll }) => {
    const { data } = useConnectors({ refetchInterval: poll ? POLL_MS : false });
    return data?.platforms?.find((p) => p.platform === platform);
  },
  useTest: () => useTestConnector(),
};

/** Source for the setup wizard, backed by the setup-scoped mirrors. */
export const setupSource: PlatformSource = {
  usePlatform: (platform, { poll }) => {
    const { data } = useQuery({
      queryKey: ["setup", "platforms"],
      queryFn: () =>
        api.get<{ platforms: ConnectorPlatform[] }>("/api/v1/setup/platforms"),
      refetchInterval: poll ? POLL_MS : false,
    });
    return data?.platforms?.find((p) => p.platform === platform);
  },
  useTest: () => {
    const qc = useQueryClient();
    return useMutation({
      mutationFn: (platform: string) =>
        api.post<TestConnectorResponse>(
          `/api/v1/setup/platforms/${platform}/test`,
        ),
      onSuccess: () => qc.invalidateQueries({ queryKey: ["setup", "platforms"] }),
    });
  },
};
