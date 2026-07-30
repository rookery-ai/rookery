import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

// Mirrors toAPISkillListItem (web/api_skills.go).
// Mirrors apiSkillMeta — the SAME fields for both kinds, parsed server-side by
// one skilllibrary.ParseMeta so a built-in skill and a created one are described
// identically. `version` may be "" (the chip is then omitted); `category` always
// resolves, defaulting to "Other".
export type SkillMeta = { category: string; version: string; requires: string[] };

export type SkillListItem = {
  id: string;
  name: string;
  description: string;
  created_at: string;
} & SkillMeta;

// Mirrors apiCoreSkillListItem.
export type CoreSkillListItem = { slug: string; name: string; description: string } & SkillMeta;

// Mirrors toAPISkillDraft — nil draft serializes as JSON null.
export type SkillDraft = {
  skill_name?: string;
  state?: string;
  updated_at?: string;
  expires_at?: string;
} | null;

// Mirrors apiGetSkill / apiSaveSkill / apiCreateSkill's response shape.
export type SkillDetail = { id: string; name: string; description: string; content: string } & SkillMeta;

// Mirrors apiGetCoreSkill's response shape.
export type CoreSkillDetail = { slug: string; content: string; description: string } & SkillMeta;

export function useSkills() {
  return useQuery({
    queryKey: ["skills"],
    queryFn: () =>
      api.get<{ skills: SkillListItem[]; core_skills: CoreSkillListItem[]; draft: SkillDraft }>(
        "/api/v1/skills",
      ),
  });
}

export function useSkillDetail(id: string | null) {
  return useQuery({
    queryKey: ["skill", id],
    queryFn: () => api.get<SkillDetail>(`/api/v1/skills/${id}`),
    enabled: !!id,
  });
}

export function useCoreSkill(slug: string | null) {
  return useQuery({
    queryKey: ["core-skill", slug],
    queryFn: () => api.get<CoreSkillDetail>(`/api/v1/skills/core/${slug}`),
    enabled: !!slug,
  });
}

export function useSkillActions() {
  const qc = useQueryClient();

  const createMut = useMutation({
    mutationFn: (content: string) => api.post<SkillDetail>("/api/v1/skills", { content }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["skills"] }),
  });

  const saveMut = useMutation({
    mutationFn: ({ id, content }: { id: string; content: string }) =>
      api.put<SkillDetail>(`/api/v1/skills/${id}`, { content }),
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: ["skills"] });
      qc.invalidateQueries({ queryKey: ["skill", id] });
    },
  });

  const delMut = useMutation({
    mutationFn: (id: string) => api.del<{ ok: boolean }>(`/api/v1/skills/${id}`),
    onSuccess: (_data, id) => {
      qc.invalidateQueries({ queryKey: ["skills"] });
      qc.removeQueries({ queryKey: ["skill", id] });
    },
  });

  return {
    create: (content: string) => createMut.mutateAsync(content),
    save: (id: string, content: string) => saveMut.mutateAsync({ id, content }),
    del: (id: string) => delMut.mutateAsync(id),
  };
}
