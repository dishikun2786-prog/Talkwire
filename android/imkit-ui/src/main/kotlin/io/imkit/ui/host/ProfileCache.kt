package io.imkit.ui.host

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * Names and avatars, asked from the host in batches and kept for the
 * session. A failed lookup is not repeated for [retryAfterMs], so a list of
 * unknown senders does not turn into a request per frame.
 */
@Stable
public class ProfileCache(
    private val host: ImUiHost,
    private val scope: CoroutineScope,
    private val clock: () -> Long = System::currentTimeMillis,
    private val retryAfterMs: Long = 60_000,
) {
    private val _profiles = MutableStateFlow<Map<String, ImProfile>>(emptyMap())
    public val profiles: StateFlow<Map<String, ImProfile>> = _profiles.asStateFlow()

    private val lock = Any()
    private val inFlight = HashSet<String>()
    private val missedAt = HashMap<String, Long>()

    init {
        scope.launch { host.contacts.collect { list -> put(list) } }
    }

    public fun put(profiles: Collection<ImProfile>) {
        if (profiles.isEmpty()) return
        _profiles.update { it + profiles.associateBy { p -> p.uid } }
    }

    /** Loads the ones not known yet; returns at once. */
    public fun request(uids: Collection<String>) {
        val now = clock()
        val wanted = synchronized(lock) {
            val known = _profiles.value
            uids.filter { uid ->
                uid.isNotEmpty() && uid !in known && uid !in inFlight && (missedAt[uid]?.let { now - it >= retryAfterMs } ?: true)
            }.toSet().also { inFlight += it }
        }
        if (wanted.isEmpty()) return
        scope.launch {
            val got = try {
                host.profiles(wanted)
            } catch (e: CancellationException) {
                synchronized(lock) { inFlight -= wanted }
                throw e
            } catch (e: Exception) {
                emptyMap()
            }
            put(got.values)
            synchronized(lock) {
                inFlight -= wanted
                val t = clock()
                for (uid in wanted) if (uid !in got) missedAt[uid] = t else missedAt.remove(uid)
            }
        }
    }

    /** Forgets [uid] so the next [request] asks again (after the person changed their name, say). */
    public fun invalidate(uid: String) {
        synchronized(lock) { missedAt.remove(uid) }
        _profiles.update { it - uid }
    }

    public fun name(uid: String): String = _profiles.value[uid]?.name?.takeIf { it.isNotBlank() } ?: shortUid(uid)

    public companion object {
        /** What is shown for someone the host does not know: a recognisable piece of the uid. */
        public fun shortUid(uid: String): String = uid.removePrefix("uid_").removePrefix("usr").take(6).ifEmpty { "?" }
    }
}

/** The profiles of [uids], loading the missing ones. */
@Composable
public fun ProfileCache.rememberProfiles(uids: Collection<String>): Map<String, ImProfile> {
    val all by profiles.collectAsState()
    LaunchedEffect(uids) { request(uids) }
    return all
}
