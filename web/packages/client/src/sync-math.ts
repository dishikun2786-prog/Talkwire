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

/**
 * Local seqs a page proves gone: the server returns every remaining message
 * with seq in `(afterSeq, upper]`, where `upper` is the last seq returned, or
 * unbounded when the page was not full (nothing comes after it). A local seq
 * in that range the page lacks was recalled while we were away (section 9.5).
 *
 * `local` must be what was held **before** the request was sent: a message
 * pushed while the request was in flight may be newer than the page.
 */
export function recalledWhileAway(local: Iterable<number>, afterSeq: number, page: readonly number[], pageWasFull: boolean): number[] {
  if (afterSeq <= 0) return [];
  if (page.length === 0 && pageWasFull) return [];
  const upper = pageWasFull ? Math.max(...page) : Infinity;
  const returned = new Set(page);
  return [...local].filter((s) => s > afterSeq && s <= upper && !returned.has(s)).sort((a, b) => a - b);
}
