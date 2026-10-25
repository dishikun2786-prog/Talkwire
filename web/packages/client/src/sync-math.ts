/** The pure parts of the sync algorithm (section 8), kept apart so they can be tested alone. */

/**
 * Where an incremental pull starts: the end of the local **contiguous** run of
 * seqs, not the largest seq (C3). With `afterSeq = max`, a hole below the
 * maximum (a dropped push, a missed subscription) would never be asked for
 * again; from the end of the run it costs one page of messages already held.
 *
 * A recalled seq is accounted for (its row was deleted on purpose), and so is
 * everything at or below the clear watermark; neither is a hole. Returns 0
 * when nothing is held: then only the newest page is fetched.
 */
export function resumeSeq(stored: Iterable<number>, recalled: Iterable<number>, clearBeforeSeq: number): number {
  const held = [...new Set([...stored, ...recalled])].filter((s) => s > clearBeforeSeq).sort((a, b) => a - b);
  // After a clear the run starts at the watermark; otherwise at the oldest
  // seq held (older history may simply never have been loaded).
  let end: number;
  if (clearBeforeSeq > 0) end = clearBeforeSeq;
  else if (held.length === 0) return 0;
  else end = held[0]!;
  for (const s of held) {
    if (s <= end) continue;
    if (s === end + 1) end = s;
    else break;
  }
  return end;
}

/** A larger gap than this between the bounds of a page is not filled in seq by seq. */
export const MAX_ACCOUNTED_GAP = 10_000;

export interface PageBounds {
  /** The seqs held before the request was sent; a message pushed while it was in flight may be newer than the page. */
  local: Iterable<number>;
  /** The request's `afterSeq`; 0 for the newest page. */
  afterSeq: number;
  /** The seqs the page returned. */
  page: readonly number[];
  /** Whether the page was full: more may come after it. */
  full: boolean;
  /** The conversation's `lastSeq` from `sync_summary` (section 8.1), when known. */
  lastSeq?: number;
  /** The clear watermark: nothing at or below it is a gap. */
  floor?: number;
}

/**
 * The seqs a page proves gone (section 8.2). The server returns every
 * remaining message with a seq in `(low, high]`: `low` is `afterSeq`, or just
 * below the first seq of the newest page (older history is only not loaded);
 * `high` is the page's last seq. When nothing comes after the page (it was not
 * full, or it is the newest page) `high` reaches `lastSeq` too, and every
 * local seq beyond it is gone as well. A seq in there the page lacks was
 * recalled: either one held here (it becomes a tombstone) or one never
 * received, which has to be accounted for all the same, or the end of the
 * local contiguous run would stay in front of it and every later sync would
 * start again from it.
 */
export function seqsGone(b: PageBounds): number[] {
  const returned = new Set(b.page);
  const floor = b.floor ?? 0;
  const newest = b.afterSeq <= 0;
  const atEnd = newest || !b.full;
  let low = newest ? (b.page.length > 0 ? Math.min(...b.page) - 1 : 0) : b.afterSeq;
  low = Math.max(low, floor);
  let high = b.page.length > 0 ? Math.max(...b.page) : low;
  if (atEnd && b.lastSeq !== undefined) high = Math.max(high, b.lastSeq);
  const gone = new Set<number>();
  if (high - low <= MAX_ACCOUNTED_GAP) {
    for (let s = low + 1; s <= high; s++) if (!returned.has(s)) gone.add(s);
  } else {
    // Too wide to list one by one: account for what is held only.
    for (const s of b.local) if (s > low && s <= high && !returned.has(s)) gone.add(s);
  }
  if (!newest && !b.full) for (const s of b.local) if (s > high && !returned.has(s)) gone.add(s);
  return [...gone].filter((s) => s > floor).sort((a, c) => a - c);
}
