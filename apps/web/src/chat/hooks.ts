import { useQuery } from "@tanstack/react-query";
import { gateway } from "../app/query";
import { useAccount } from "../data/account";
import type { DailyBriefing, LatestBriefingRead } from "./types";

const BUSINESS_DAY = /^\d{4}-\d{2}-\d{2}$/;
const RFC3339_INSTANT = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isValidBusinessDay(value: unknown): value is string {
  if (typeof value !== "string" || !BUSINESS_DAY.test(value)) return false;
  const instant = new Date(`${value}T00:00:00Z`);
  return !Number.isNaN(instant.getTime()) && instant.toISOString().slice(0, 10) === value;
}

function isValidInstant(value: unknown): value is string {
  if (typeof value !== "string" || !RFC3339_INSTANT.test(value)) return false;
  if (!isValidBusinessDay(value.slice(0, 10))) return false;

  const hour = Number(value.slice(11, 13));
  const minute = Number(value.slice(14, 16));
  const second = Number(value.slice(17, 19));
  return hour <= 23 && minute <= 59 && second <= 59 && !Number.isNaN(new Date(value).getTime());
}

function parseLatestBriefingRead(
  value: unknown,
  marketplaceAccountId: string,
  beforeBusinessDay: string,
): LatestBriefingRead {
  if (!isRecord(value)) throw new Error("invalid_latest_briefing_provenance");

  if (
    value.state === "never_generated" &&
    value.provenance === "none" &&
    value.briefing === undefined
  ) {
    return value as LatestBriefingRead;
  }

  const briefing = value.briefing;
  if (
    value.state !== "available" ||
    value.provenance !== "stored_briefing" ||
    !isRecord(briefing) ||
    briefing.marketplaceAccountId !== marketplaceAccountId ||
    !isValidBusinessDay(briefing.businessDay) ||
    briefing.businessDay >= beforeBusinessDay ||
    !isValidInstant(briefing.generatedAt) ||
    !Array.isArray(briefing.events)
  ) {
    throw new Error("invalid_latest_briefing_provenance");
  }

  return value as LatestBriefingRead;
}

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
      const data: unknown = res.data;
      if (data === undefined) throw new Error("latest_briefing_unavailable");
      return parseLatestBriefingRead(data, marketplaceAccountId, beforeBusinessDay);
    },
    enabled,
    retry: false,
  });
}
