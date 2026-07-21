import { useQuery } from "@tanstack/react-query";
import { gateway } from "../app/query";
import { useAccount } from "../data/account";
import type { DailyBriefing, LatestBriefingRead } from "./types";

// The stored daily briefing (CHAT-010): a READ that never generates. Its events
// carry the SAME ids + order as the Today feed. The businessDay is the UTC
// calendar date; on error the dock renders the §16 dated-last-briefing failure
// state — Today stays current regardless.

/** UTC calendar date (YYYY-MM-DD) for `now`, matching the briefing's businessDay. */
export function utcBusinessDay(now: Date = new Date()): string {
  return now.toISOString().slice(0, 10);
}

export function useBriefing(businessDay: string) {
  const { marketplaceAccountId } = useAccount();
  return useQuery({
    queryKey: ["briefing", marketplaceAccountId, businessDay] as const,
    queryFn: async (): Promise<DailyBriefing> => {
      const res = await gateway.GET("/briefing", {
        params: { query: { marketplaceAccountId, businessDay } },
      });
      if (res.error) {
        const env = res.error as { code?: string; message?: string };
        throw new Error(env.message ?? env.code ?? "briefing_unavailable");
      }
      if (res.data === undefined) throw new Error("briefing_unavailable");
      return res.data;
    },
    retry: false,
  });
}

export function useLatestBriefingBefore(beforeBusinessDay: string, enabled: boolean) {
  const { marketplaceAccountId } = useAccount();
  return useQuery({
    queryKey: ["briefing", "latest", marketplaceAccountId, beforeBusinessDay] as const,
    queryFn: async (): Promise<LatestBriefingRead> => {
      const res = await gateway.GET("/briefing/latest", {
        params: { query: { marketplaceAccountId, beforeBusinessDay } },
      });
      if (res.error) {
        const env = res.error as { code?: string; message?: string };
        throw new Error(env.message ?? env.code ?? "latest_briefing_unavailable");
      }
      const data = res.data;
      if (data === undefined) throw new Error("latest_briefing_unavailable");

      // The generated type cannot encode cross-field OpenAPI conditions. Validate
      // the owned boundary so malformed provenance fails closed instead of
      // becoming a date claim.
      if (
        data.state === "available" &&
        data.provenance === "stored_briefing" &&
        data.briefing !== undefined
      ) {
        return data;
      }
      if (
        data.state === "never_generated" &&
        data.provenance === "none" &&
        data.briefing === undefined
      ) {
        return data;
      }
      throw new Error("invalid_latest_briefing_provenance");
    },
    enabled,
    retry: false,
  });
}
