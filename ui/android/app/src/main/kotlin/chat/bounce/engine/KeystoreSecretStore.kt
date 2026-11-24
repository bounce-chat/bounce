package chat.bounce.engine

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import chat.bounce.goengine.SecretStore
import java.io.File
import java.io.FileNotFoundException
import java.io.FileOutputStream
import java.security.KeyStore
import java.security.UnrecoverableKeyException
import javax.crypto.AEADBadTagException
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * The install's keystore secret, kept the way the Android Keystore allows.
 *
 * The Keystore stores nothing of ours. It holds an AES key that never leaves
 * it and encrypts or decrypts with that key on request, so the engine's secret
 * is handed to it, and the ciphertext that comes back is what this class keeps,
 * in [file] beside the install's other files. The file is useless without the
 * Keystore key, and the key is useless on any other device.
 *
 * The key is deliberately not bound to user authentication: the service has to
 * open the install after a reboot with nobody there to unlock anything.
 *
 * The engine tells three answers apart, and [get] gives them like this (see
 * SecretStore in goengine/security.go):
 *
 *  - the secret;
 *  - null, when there is no secret here that this device can read: no file, no
 *    key, or a file sealed by some other device's key. That is what a restored
 *    or copied data folder looks like, and the engine answers it by asking for
 *    the recovery code;
 *  - an exception, when the Keystore could not be asked. The engine reports
 *    that and the user tries again.
 *
 * THREADING - both methods are called by Go, on a Go-owned thread, from inside
 * Engine.start or Goengine.recover. They may block, and they must not touch
 * anything a thread waiting on that call could be holding.
 */
class KeystoreSecretStore(private val file: File) : SecretStore {

    override fun get(): ByteArray? {
        val sealed = try {
            file.readBytes()
        } catch (e: FileNotFoundException) {
            return null
        }
        if (sealed.size <= IV_BYTES) return null

        val key = try {
            keyStore().getKey(KEY_ALIAS, null) as? SecretKey
        } catch (e: UnrecoverableKeyException) {
            // The entry is there and can never be used again, which waiting
            // will not change.
            null
        } ?: return null

        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(TAG_BITS, sealed, 0, IV_BYTES))
        return try {
            cipher.doFinal(sealed, IV_BYTES, sealed.size - IV_BYTES)
        } catch (e: AEADBadTagException) {
            // Sealed by a different key: a folder that came from another device
            // or from before the app was reinstalled.
            null
        }
    }

    override fun store(secret: ByteArray) {
        val key = keyStore().getKey(KEY_ALIAS, null) as? SecretKey ?: generateKey()

        // The Keystore insists on choosing the nonce itself, so it is read back
        // from the cipher and kept in front of the ciphertext.
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key)
        check(cipher.iv.size == IV_BYTES) { "the Keystore chose a nonce of ${cipher.iv.size} bytes" }
        val sealed = cipher.iv + cipher.doFinal(secret)

        // Written beside the file and renamed over it, so a crash leaves the
        // old contents or the new and never half of either. The engine reads
        // the secret back before it relies on it.
        val temporary = File(file.parentFile, file.name + ".tmp")
        FileOutputStream(temporary).use { out ->
            out.write(sealed)
            out.fd.sync()
        }
        if (!temporary.renameTo(file)) {
            temporary.delete()
            throw java.io.IOException("could not replace ${file.name}")
        }
    }

    private fun keyStore(): KeyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }

    private fun generateKey(): SecretKey {
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(
                KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(KEY_BITS)
                .build(),
        )
        return generator.generateKey()
    }

    private companion object {
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val KEY_ALIAS = "chat.bounce.install_secret"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val KEY_BITS = 256
        const val IV_BYTES = 12
        const val TAG_BITS = 128
    }
}
