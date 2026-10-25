package io.imkit.push.fcm

import com.google.android.gms.tasks.Task
import com.google.firebase.messaging.FirebaseMessaging
import io.imkit.core.notify.PushTokenSource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.suspendCancellableCoroutine
import java.util.concurrent.Executor
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/**
 * The FCM token for [io.imkit.core.notify.PushRegistration], which registers
 * it after every login (spec 17). Null when Play services or Firebase are not
 * available, so a device without them simply gets no offline push.
 */
public class FcmTokenSource internal constructor(private val fetch: () -> Task<String>) : PushTokenSource {
    public constructor() : this({ FirebaseMessaging.getInstance().token })

    override val provider: String = "fcm"

    override suspend fun token(): String? = try {
        fetch().await()
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        null
    }
}

/** Without kotlinx-coroutines-play-services; the listener runs where the task completes, not on the main thread. */
internal suspend fun <T> Task<T>.await(): T = suspendCancellableCoroutine { cont ->
    addOnCompleteListener(Executor { it.run() }) { t ->
        when {
            t.isCanceled -> cont.cancel()
            t.isSuccessful -> cont.resume(t.result)
            else -> cont.resumeWithException(t.exception ?: IllegalStateException("task failed"))
        }
    }
}
