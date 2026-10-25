package io.imkit.core.sync

import io.imkit.core.model.PresenceRecord
import io.imkit.core.model.PresenceState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

/**
 * The single source of truth for presence (C5). The conversation list and
 * the chat screen both read [records]; nothing else keeps an online flag.
 *
 * Three states: online, offline and unknown. Unknown is not offline: it is
 * "never heard" or "heard too long ago" ([ttlMs]), so a stale record shows no
 * status instead of a wrong one. Frames are ordered by the server's
 * `presRev`; an older revision arriving late is dropped (spec 12.1).
 */
public class PresenceTracker(
    private val ttlMs: Long = 180_000L,
    private val clock: () -> Long = System::currentTimeMillis,
) {
    private val _records = MutableStateFlow<Map<String, PresenceRecord>>(emptyMap())
    public val records: StateFlow<Map<String, PresenceRecord>> = _records.asStateFlow()

    /** Applies a push or one entry of a snapshot. */
    public fun apply(uid: String, online: Boolean, rev: Long, lastSeenAt: Long = 0) {
        if (uid.isEmpty()) return
        val now = clock()
        _records.update { cur ->
            val prev = cur[uid]
            if (prev != null && rev > 0 && prev.rev > rev) return@update cur
            cur + (
                uid to PresenceRecord(
                    online = online,
                    rev = if (rev > 0) rev else prev?.rev ?: 0,
                    receivedAt = now,
                    lastSeenAt = if (lastSeenAt > 0) lastSeenAt else prev?.lastSeenAt ?: 0,
                )
                )
        }
    }

    public fun state(uid: String, now: Long = clock()): PresenceState {
        val r = _records.value[uid] ?: return PresenceState.UNKNOWN
        if (now - r.receivedAt > ttlMs) return PresenceState.UNKNOWN
        return if (r.online) PresenceState.ONLINE else PresenceState.OFFLINE
    }

    /** Last time the user went offline, 0 when unknown. */
    public fun lastSeenAt(uid: String): Long = _records.value[uid]?.lastSeenAt ?: 0

    public fun clear() {
        _records.value = emptyMap()
    }
}
