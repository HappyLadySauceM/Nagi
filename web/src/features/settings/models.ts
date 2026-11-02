import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../../lib/api/client";
export type Usage = "generation" | "intent" | "asr" | "tts";
export interface ModelProfile {
  ID: string;
  Name: string;
  Usage: Usage;
  Protocol: string;
  BaseURL: string;
  Model: string;
  Images: boolean;
  Tools: boolean;
  Voice: string;
  Thinking?: boolean;
  ReasoningEffort?: string;
  ContextWindow?: number;
  MaxOutputTokens: number;
  TimeoutSeconds: number;
  Version: number;
  VerifiedVersion?: number;
  VerifiedAt?: string;
  HTTPHeaders?: Record<string, string>;
  HasCredential: boolean;
}
export interface ModelView {
  Catalog: {
    Profiles: ModelProfile[];
    Defaults: Partial<Record<Usage, string>>;
    Selections: Record<string, string>;
    HasBilibiliCredential: boolean;
  };
  Version: number;
}
export function useModels() {
  return useQuery({
    queryKey: ["models"],
    queryFn: async ({ signal }) => {
      const raw = await apiRequest<ModelView>("/api/v1/models", { signal });
      if (
        !raw?.Catalog ||
        !Array.isArray(raw.Catalog.Profiles) ||
        !raw.Catalog.Defaults ||
        !raw.Catalog.Selections ||
        typeof raw.Version !== "number"
      )
        throw new Error("模型配置响应格式不正确");
      return raw;
    },
  });
}
export function modelReady(
  catalog: ModelView["Catalog"] | undefined,
  usage: Usage,
  bot = 0,
  kind = "",
  target = 0,
) {
  if (!catalog) return false;
  let id = catalog.Defaults[usage];
  if (usage === "generation")
    id =
      catalog.Selections[`${bot}:${kind}:${target}`] ||
      catalog.Selections[`${bot}:${kind}:0`] ||
      id;
  return catalog.Profiles.some(
    (p) =>
      p.ID === id &&
      p.Usage === usage &&
      p.VerifiedVersion === p.Version &&
      Boolean(p.VerifiedAt),
  );
}
