package io.talkwire.storage.room

import java.security.SecureRandom
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** A data key encrypted by a [KeyWrapper]: the IV and the ciphertext it produced. */
public class WrappedKey(public val iv: ByteArray, public val ciphertext: ByteArray)

/**
 * Wraps the per-conversation data keys with a key the database never holds,
 * such as a non-exportable key in the Android Keystore (talkwire-android has
 * `AndroidKeystoreKeyWrapper`). With a wrapper, message text, `extra` and
 * drafts are sealed with AES-GCM before they are written; deleting a
 * conversation's messages destroys its data key, so whatever bytes remain on
 * disk can no longer be read ("crypto-shredding").
 *
 * How strong the shredding is depends on the wrapper: a Keystore key backed by
 * secure hardware cannot be copied out; a software Keystore, or a key kept in
 * a file, only raises the bar.
 */
public interface KeyWrapper {
    public fun wrap(key: ByteArray): WrappedKey

    public fun unwrap(wrapped: WrappedKey): ByteArray
}

/** AES-256-GCM sealing of text with a conversation's data key. */
internal object ContentCrypto {
    private const val TRANSFORMATION = "AES/GCM/NoPadding"
    private const val TAG_BITS = 128
    private const val PREFIX = "v1:"
    private val random = SecureRandom()

    fun newDataKey(): ByteArray = ByteArray(32).also { random.nextBytes(it) }

    /** "v1:" + base64(iv) + ":" + base64(ciphertext). A fresh IV every time: GCM must never reuse one under a key. */
    fun seal(key: SecretKey, plain: String): String {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key)
        val ct = cipher.doFinal(plain.toByteArray(Charsets.UTF_8))
        val b64 = Base64.getEncoder()
        return PREFIX + b64.encodeToString(cipher.iv) + ":" + b64.encodeToString(ct)
    }

    /** Null when the text is not sealed this way or the key does not open it. */
    fun open(key: SecretKey, sealed: String): String? {
        if (!sealed.startsWith(PREFIX)) return null
        val parts = sealed.substring(PREFIX.length).split(':')
        if (parts.size != 2) return null
        return try {
            val b64 = Base64.getDecoder()
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(TAG_BITS, b64.decode(parts[0])))
            String(cipher.doFinal(b64.decode(parts[1])), Charsets.UTF_8)
        } catch (e: Exception) {
            null
        }
    }
}
