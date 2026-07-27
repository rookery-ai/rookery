import { QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "react-router";
import { router } from "./router";
import { ApiError } from "@/lib/api";
import { TimeZoneProvider } from "@/lib/timezone";

const AUTH_ERROR_CODES = ["not_authenticated", "no_workspace", "needs_setup", "must_change_password"];

const qc: QueryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (err, query) => {
      if (query.queryKey[0] === "session") return;
      if (err instanceof ApiError && AUTH_ERROR_CODES.includes(err.code)) {
        qc.invalidateQueries({ queryKey: ["session"] });
      }
    },
  }),
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false } },
});

export default function App() {
  return (
    <QueryClientProvider client={qc}>
      <TimeZoneProvider>
        <RouterProvider router={router} />
      </TimeZoneProvider>
    </QueryClientProvider>
  );
}
