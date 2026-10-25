package io.imkit.storage.room

import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/** A software stand-in for the Keystore wrapper (Robolectric has no AndroidKeyStore). */
class TestKeyWrapper(private val failWrap: Boolean = false) : KeyWrapper {
    private val kek = SecretKeySpec(ByteArray(32) { it.toByte() }, "AES")
    var wraps = 0
        private set

    override fun wrap(key: ByteArray): WrappedKey {
        if (failWrap) error("keystore unavailable")
        wraps++
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, kek)
        return WrappedKey(c.iv, c.doFinal(key))
    }

    override fun unwrap(wrapped: WrappedKey): ByteArray {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.DECRYPT_MODE, kek, GCMParameterSpec(128, wrapped.iv))
        return c.doFinal(wrapped.ciphertext)
    }
}
