package io.imkit.call.dingrtc

import android.content.Context
import android.graphics.PixelFormat
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.view.SurfaceHolder
import android.view.View
import org.webrtc.mozi.video.view.RTCRenderViewDelegate
import org.webrtc.mozi.video.view.RTCSurfaceView

/**
 * DingRTC 3.0's render SurfaceView without its four null dereferences.
 *
 * `RTCSurfaceView` dereferences its render delegate in `onMeasure`,
 * `surfaceCreated`, `surfaceChanged` and `surfaceDestroyed` with no null
 * check, while the delegate is only attached later, by the SDK's native side,
 * when the view is bound. A view measured before that crashes the process
 * (a video call that crashed on answering, in the reference deployment). The
 * SDK's own factory (`createRenderSurfaceView`) builds exactly such an
 * unbound view; `RTCTextureView` has the same holes.
 *
 * This subclass watches the attach itself (`attachRenderDelegate` comes from
 * a public interface): until then `onMeasure` uses the default size and the
 * surface callbacks are skipped, and those skipped are replayed once the
 * delegate arrives. It also asks for a new layout then: the SDK hands the
 * view's size to the GL viewport only from its `measureSize`, which the
 * skipped measures never called, and without it the picture stays black.
 * Once bound, every method takes the SDK's own path.
 *
 * The delegate is attached exactly once and never detached (its only caller
 * passes `this`), so tearing down a bound view needs no guard.
 */
internal class SafeRtcSurfaceView(context: Context) : RTCSurfaceView(context.applicationContext) {

    /** Written on the SDK's thread, read on the main thread. */
    @Volatile
    private var delegateAttached = false

    /**
     * A surface callback was skipped before the delegate came. Only the main
     * thread reads and writes it: the attach posts to the main thread
     * unconditionally and the check happens there, so no interleaving loses
     * the replay. [pendingFormat], [pendingWidth] and [pendingHeight] are
     * written before it.
     */
    @Volatile
    private var pendingSurfaceReady = false
    private var pendingFormat = PixelFormat.RGBA_8888
    private var pendingWidth = 0
    private var pendingHeight = 0

    override fun attachRenderDelegate(delegate: RTCRenderViewDelegate?) {
        super.attachRenderDelegate(delegate)
        delegateAttached = delegate != null
        if (delegate == null) return
        // The replay goes into the SDK's updateSurfaceSize, which requires the main
        // thread. A Handler, not View.post: posting from another thread before the
        // view is attached races on View's lazily created run queue.
        mainHandler.post { onDelegateAttachedOnMainThread() }
    }

    private fun onDelegateAttachedOnMainThread() {
        if (!delegateAttached) return
        requestLayout()
        replayPendingSurfaceCallbacks()
    }

    private fun replayPendingSurfaceCallbacks() {
        if (!delegateAttached || !pendingSurfaceReady) return
        val h = holder
        // The surface may be gone meanwhile; surfaceCreated comes again when the view returns.
        if (h == null || h.surface?.isValid != true) return
        pendingSurfaceReady = false
        val format = pendingFormat
        val w = pendingWidth
        val ht = pendingHeight
        pendingWidth = 0
        pendingHeight = 0
        runCatching { superSurfaceCreated(h) }.onFailure { Log.w(TAG, "replay surfaceCreated failed: ${it.message}") }
        if (w > 0 && ht > 0) runCatching { superSurfaceChanged(h, format, w, ht) }.onFailure { Log.w(TAG, "replay surfaceChanged failed: ${it.message}") }
    }

    // Kotlin has no super calls inside lambdas.
    private fun superSurfaceCreated(h: SurfaceHolder) = super.surfaceCreated(h)

    private fun superSurfaceChanged(h: SurfaceHolder, format: Int, w: Int, ht: Int) = super.surfaceChanged(h, format, w, ht)

    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        if (!delegateAttached) {
            setDefaultMeasuredDimension(widthMeasureSpec, heightMeasureSpec)
            return
        }
        try {
            super.onMeasure(widthMeasureSpec, heightMeasureSpec)
        } catch (t: Throwable) {
            // Rebinding releases the old delegate just before the new one: a measure in
            // that window may throw anything. A measure must never take the process down.
            Log.w(TAG, "onMeasure fell back to the default size: ${t.message}")
            setDefaultMeasuredDimension(widthMeasureSpec, heightMeasureSpec)
        }
    }

    private fun setDefaultMeasuredDimension(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        setMeasuredDimension(View.getDefaultSize(0, widthMeasureSpec), View.getDefaultSize(0, heightMeasureSpec))
    }

    override fun surfaceCreated(holder: SurfaceHolder) {
        if (!delegateAttached) {
            pendingSurfaceReady = true
            return
        }
        runCatching { super.surfaceCreated(holder) }.onFailure { Log.w(TAG, "surfaceCreated failed: ${it.message}") }
    }

    override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
        if (!delegateAttached) {
            // The sizes first, the volatile flag last: its write publishes them.
            pendingFormat = format
            pendingWidth = width
            pendingHeight = height
            pendingSurfaceReady = true
            return
        }
        runCatching { super.surfaceChanged(holder, format, width, height) }.onFailure { Log.w(TAG, "surfaceChanged failed: ${it.message}") }
    }

    override fun surfaceDestroyed(holder: SurfaceHolder) {
        pendingSurfaceReady = false
        pendingWidth = 0
        pendingHeight = 0
        if (!delegateAttached) return
        runCatching { super.surfaceDestroyed(holder) }.onFailure { Log.w(TAG, "surfaceDestroyed failed: ${it.message}") }
    }

    private companion object {
        const val TAG = "SafeRtcSurfaceView"
        val mainHandler = Handler(Looper.getMainLooper())
    }
}
