package io.talkwire.android.media

import android.net.Uri
import java.io.File

/**
 * Makes a picked video smaller before [MediaSender] uploads it. The
 * implementation with Media3 Transformer lives in the optional
 * `talkwire-video-compress` module, so apps that never send large videos do
 * not pull in the encoder stack.
 */
public fun interface VideoCompressor {
    /**
     * Writes a re-encoded copy of [source] to [out]. Returns false when the
     * video cannot be re-encoded; [MediaSender] then decides from the
     * original's size. Cancelling the caller must stop the work and must not
     * leave a half-written file behind.
     */
    public suspend fun compress(source: Uri, out: File): Boolean
}
