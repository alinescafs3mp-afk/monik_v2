import { computed, inject, type InjectionKey, type Ref } from 'vue';

export type SessionUser = Record<string, unknown> | null;
export const sessionKey: InjectionKey<Ref<SessionUser>> = Symbol('monik-session');
/** Presentation permissions follow the current authenticated session. The API remains authoritative. */
export function useSession() {
  const me = inject(sessionKey);
  return { me, owner: computed(() => me?.value?.role === 'owner') };
}
