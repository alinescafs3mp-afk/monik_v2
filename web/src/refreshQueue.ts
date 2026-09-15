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


/** Keep background I/O separate from a user's explicit refresh affordance. */
export function createPollingController(load: () => Promise<void>, publish: (s: {loading:boolean;refreshing:boolean;busy:boolean;error:string;updated:Date|null}) => void) {
  let disposed = false, started = false, loading = true, refreshing = false;
  let error = "", updated: Date | null = null;
  const emit = () => { if (!disposed) publish({loading, refreshing, busy:started, error, updated}); };
  const queue = createRefreshQueue(async () => {
    if (disposed) return;
    started = true; emit();
    try { await load(); if (!disposed) { error = ""; updated = new Date(); } }
    catch (e) { if (!disposed) error = (e as Error)?.message || "Не удалось получить данные"; }
    finally { if (!disposed) { loading = false; emit(); } }
  });
  async function request(manual: boolean) {
    if (disposed) return;
    // Timer/SSE hints do not grow a queue while a slow request is running.
    if (!manual && started) return;
    started = true;
    if (manual) refreshing = true;
    emit();
    try { await queue.refresh(); }
    finally { if (!disposed) { started = false; refreshing = false; emit(); } }
  }
  return { refresh: () => request(true), background: () => request(false), dispose() {disposed = true; queue.dispose();} };
}
