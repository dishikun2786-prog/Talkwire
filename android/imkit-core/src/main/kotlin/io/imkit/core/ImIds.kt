package io.imkit.core

import java.security.MessageDigest
import java.util.Base64

/**
 * Identities on the wire (protocol spec 5).
 *
 * The algorithms are fixed: every server, host and client computes the same
 * strings, and the golden vectors in `contract/host-api/uid-vectors.json` pin
 * them. Changing either function invalidates every stored uid and topic.
 */
public object ImIds {
    private val b64 = Base64.getUrlEncoder().withoutPadding()

    /** `"uid_" + base64url(SHA-256(UTF-8 appUserId)[0:8])`, 15 characters. */
    public fun uid(appUserId: String): String = "uid_" + b64.encodeToString(sha256(appUserId).copyOf(8))

    /**
     * The canonical direct-chat topic of two uids: `"p2p_" + base64url(SHA-256(a + ":" + b)[0:8])`
     * with `a`, `b` sorted by bytes. Argument order does not matter.
     */
    public fun p2pTopic(uidA: String, uidB: String): String {
        val (a, b) = if (compareBytes(uidA, uidB) <= 0) uidA to uidB else uidB to uidA
        return "p2p_" + b64.encodeToString(sha256("$a:$b").copyOf(8))
    }

    /** A user uid: `uid_…`, or the legacy `usr…` form the server still accepts. */
    public fun isUserUid(s: String): Boolean = s.startsWith("uid_") || s.startsWith("usr")

    public fun isGroupTopic(s: String): Boolean = s.startsWith("grp_")

    public fun isP2pTopic(s: String): Boolean = s.startsWith("p2p_")

    private fun sha256(s: String): ByteArray = MessageDigest.getInstance("SHA-256").digest(s.toByteArray(Charsets.UTF_8))

    private fun compareBytes(x: String, y: String): Int {
        val a = x.toByteArray(Charsets.UTF_8)
        val b = y.toByteArray(Charsets.UTF_8)
        for (i in 0 until minOf(a.size, b.size)) {
            val d = (a[i].toInt() and 0xff) - (b[i].toInt() and 0xff)
            if (d != 0) return d
        }
        return a.size - b.size
    }
}
