/** Coalesce explicit refreshes without dropping the newest filter change.
 * Periodic callers should skip while busy; a user refresh requests one rerun. */
export function createRefreshQueue(load: () => Promise<void>) {
  let active: Promise<void> | undefined, queued = false, disposed = false;
  function refresh(): Promise<void> {
    if (disposed) return Promise.resolve();
    queued = true;
    if (active) return active;
    active = (async () => {
      try {
        while (queued && !disposed) { queued = false; await Promise.resolve().then(load); }
      } finally { active = undefined; }
    })();
    return active;
  }
  return { refresh, dispose() { disposed = true; queued = false; } };
}

export function isAuthenticationFailure(error: unknown): boolean {
  return typeof error === 'object' && error !== null && 'status' in error && error.status === 401;
}
