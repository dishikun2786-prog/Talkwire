@file:androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)

package io.talkwire.android.media

import android.content.Context
import androidx.media3.database.DatabaseProvider
import androidx.media3.database.StandaloneDatabaseProvider
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.cache.Cache
import androidx.media3.datasource.cache.CacheDataSource
import androidx.media3.datasource.cache.LeastRecentlyUsedCacheEvictor
import androidx.media3.datasource.cache.SimpleCache
import java.io.File

/**
 * The disk cache for voice and video messages (C7): the second play reads
 * from disk instead of downloading again. Give [dataSourceFactory] to the
 * player (ExoPlayer's `DefaultMediaSourceFactory(factory)`).
 *
 * The media server must send `Content-Length`: ExoPlayer asks the cache not
 * to store responses of unknown length, so a chunked response is fetched
 * again on every play. (This was the reason the source app's cache stayed
 * empty until its media route was fixed.)
 *
 * SimpleCache locks its folder, so there is one instance per process: use
 * [get].
 */
public class MediaCache internal constructor(public val cache: Cache) {
    /** Reads through the cache, falling back to [upstream] on a miss. */
    public fun dataSourceFactory(upstream: DataSource.Factory): CacheDataSource.Factory =
        CacheDataSource.Factory()
            .setCache(cache)
            .setUpstreamDataSourceFactory(upstream)
            .setFlags(CacheDataSource.FLAG_IGNORE_CACHE_ON_ERROR)

    public companion object {
        public const val DEFAULT_MAX_BYTES: Long = 300L * 1024 * 1024

        @Volatile private var instance: MediaCache? = null

        /** The process-wide cache in `cacheDir/talkwire-media`, least recently used evicted beyond [maxBytes]. */
        public fun get(context: Context, maxBytes: Long = DEFAULT_MAX_BYTES): MediaCache =
            instance ?: synchronized(this) {
                instance ?: create(File(context.cacheDir, "talkwire-media"), maxBytes, StandaloneDatabaseProvider(context.applicationContext))
                    .also { instance = it }
            }

        internal fun create(dir: File, maxBytes: Long, database: DatabaseProvider): MediaCache =
            MediaCache(SimpleCache(dir, LeastRecentlyUsedCacheEvictor(maxBytes), database))

        /** Releases the process-wide cache (for example on logout, before clearing it). */
        public fun release() {
            synchronized(this) {
                instance?.cache?.release()
                instance = null
            }
        }
    }
}
