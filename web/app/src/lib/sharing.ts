// Sharing (ADR-0006): members of the owner's account, pending invitations
// and per-hive grants (member × camera × role).
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { unwrap, type components } from "@camhive/api";
import { api } from "./api";

export type Grant = components["schemas"]["Grant"];
export type Member = { id: string; email: string; status: string; since: string };
export type Invitation = { id: string; email: string; expires_at: string };

export function useMembers() {
  return useQuery({
    queryKey: ["members"],
    queryFn: async () => unwrap(await api.GET("/v1/members")),
  });
}

export function useGrants(hiveID: string | undefined, enabled = true) {
  return useQuery({
    queryKey: ["grants", hiveID],
    queryFn: async () => unwrap(await api.GET("/v1/hives/{id}/grants", { params: { path: { id: hiveID! } } })).grants,
    enabled: !!hiveID && enabled,
  });
}

/** Mutations that keep the member and grant lists fresh. */
export function useSharingActions() {
  const qc = useQueryClient();
  const refresh = () => Promise.all([qc.invalidateQueries({ queryKey: ["members"] }), qc.invalidateQueries({ queryKey: ["grants"] }), qc.invalidateQueries({ queryKey: ["hives"] })]);
  return {
    async invite(email: string) {
      const r = unwrap(await api.POST("/v1/members/invitations", { body: { email } }));
      await refresh();
      return r;
    },
    async cancelInvitation(id: string) {
      unwrap(await api.DELETE("/v1/members/invitations/{id}", { params: { path: { id } } }));
      await refresh();
    },
    async removeMember(id: string) {
      unwrap(await api.DELETE("/v1/members/{id}", { params: { path: { id } } }));
      await refresh();
    },
    async accept(token: string) {
      const r = unwrap(await api.POST("/v1/invitations/accept", { body: { token } }));
      await refresh();
      return r;
    },
    async setGrant(hiveID: string, g: { member_id: string; camera_id: string; role: "viewer" | "operator"; expires_at?: string }) {
      const r = unwrap(await api.PUT("/v1/hives/{id}/grants", { params: { path: { id: hiveID } }, body: g }));
      await refresh();
      return r;
    },
    async deleteGrant(hiveID: string, gid: string) {
      unwrap(await api.DELETE("/v1/hives/{id}/grants/{gid}", { params: { path: { id: hiveID, gid } } }));
      await refresh();
    },
  };
}
