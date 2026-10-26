package io.talkwire.android.crypto

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import io.talkwire.storage.room.KeyWrapper
import io.talkwire.storage.room.WrappedKey
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Wraps the Room store's data keys with an AES-256 key that lives in the
 * Android Keystore and never leaves it (the source app's scheme). The key
 * does not require the device to be unlocked, so messages arriving in the
 * background can be stored.
 *
 * Strength: hardware-backed on devices with a TEE or StrongBox; some low-end
 * devices emulate the Keystore in software, where destroying a data key is
 * only as final as the file system makes it.
 */
public class AndroidKeystoreKeyWrapper internal constructor(
    private val keyEncryptionKey: () -> SecretKey,
) : KeyWrapper {
    public constructor(alias: String = DEFAULT_ALIAS) : this({ keystoreKey(alias) })

    override fun wrap(key: ByteArray): WrappedKey {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, keyEncryptionKey()) // the provider picks a fresh IV
        return WrappedKey(cipher.iv, cipher.doFinal(key))
    }

    override fun unwrap(wrapped: WrappedKey): ByteArray {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, keyEncryptionKey(), GCMParameterSpec(TAG_BITS, wrapped.iv))
        return cipher.doFinal(wrapped.ciphertext)
    }

    public companion object {
        public const val DEFAULT_ALIAS: String = "talkwire_chat_content_kek"
        private const val PROVIDER = "AndroidKeyStore"
        private const val TRANSFORMATION = "AES/GCM/NoPadding"
        private const val TAG_BITS = 128

        @Synchronized
        private fun keystoreKey(alias: String): SecretKey {
            val store = KeyStore.getInstance(PROVIDER).apply { load(null) }
            (store.getKey(alias, null) as? SecretKey)?.let { return it }
            val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, PROVIDER)
            generator.init(
                KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                    .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                    .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .setKeySize(256)
                    .setUserAuthenticationRequired(false)
                    .build(),
            )
            return generator.generateKey()
        }
    }
}
