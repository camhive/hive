// The ways in when Camhive cannot reach the hive (#57, ADR-0023): the page
// offers the hive's own sign-in where the hive answers on the LAN, and the
// manual LAN address where neither its name nor a remembered address does.
import type { Hive } from "./hives";
import { direct, openableByAddress, type Decision } from "./transport";

export type GrantState = { grant?: unknown; local: boolean; localSession: boolean; canSignInLocally: boolean };

export function emergencyOffers(g: GrantState, owned: boolean, decision: Decision | null, hive: Pick<Hive, "lan">) {
  // No way in through the cloud, and the hive is the account's own.
  const emergency = ((g.canSignInLocally && !g.grant) || (g.local && !g.localSession)) && owned;
  return {
    signIn: emergency && !g.grant && direct(decision),
    // Neither the name nor a remembered address answers; the browser must be sent to the address first.
    address: emergency && !!decision && !direct(decision) && openableByAddress(hive),
  };
}
