package io.imkit.core.m1

import io.imkit.core.TestFiles
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.Assume.assumeTrue
import java.io.File
import java.net.ServerSocket
import java.security.SecureRandom
import java.util.Base64
import java.util.concurrent.TimeUnit
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec

/**
 * A real `imkit-server` process in standalone mode (no host process) on a
 * throwaway PostgreSQL database. Needs `server/bin/imkit-server(.exe)` and
 * IMKIT_TEST_DSN (libpq key=value, the same variable the server's own
 * database tests use); without either the tests are skipped. The DSN's
 * password is handed to the server through its environment and never logged.
 *
 * Every run uses fresh user ids, so the database does not need to be empty.
 */
internal class RealServer private constructor(
    private val process: Process,
    val port: Int,
    private val secret: ByteArray,
    private val log: StringBuffer,
    private val configFile: File,
) : AutoCloseable {
    val wsUrl: String get() = "ws://127.0.0.1:$port/ws/im"
    val httpUrl: String get() = "http://127.0.0.1:$port"

    /** The server's last log lines, for failure messages. */
    fun logTail(lines: Int = 60): String = log.toString().lines().takeLast(lines).joinToString("\n")

    /** An HS256 access token for app user [sub], as the host would issue it. */
    fun token(sub: String, type: String = "access", role: String = "app_user"): String {
        fun b64(b: ByteArray) = Base64.getUrlEncoder().withoutPadding().encodeToString(b)
        val now = System.currentTimeMillis() / 1000
        val header = b64("""{"alg":"HS256","typ":"JWT"}""".toByteArray())
        val payload = b64("""{"sub":"$sub","type":"$type","role":"$role","iat":$now,"exp":${now + 3600}}""".toByteArray())
        val mac = Mac.getInstance("HmacSHA256").apply { init(SecretKeySpec(secret, "HmacSHA256")) }
        return "$header.$payload." + b64(mac.doFinal("$header.$payload".toByteArray()))
    }

    override fun close() {
        process.destroy()
        if (!process.waitFor(5, TimeUnit.SECONDS)) process.destroyForcibly()
        configFile.delete()
    }

    companion object {
        private val serverDir = File(TestFiles.repoRoot, "server")
        private val binary = File(
            serverDir,
            if (System.getProperty("os.name").startsWith("Windows")) "bin/imkit-server.exe" else "bin/imkit-server",
        )

        /** libpq key=value pairs; values may be single-quoted with backslash escapes. */
        internal fun parseDsn(dsn: String): Map<String, String> {
            val out = LinkedHashMap<String, String>()
            val re = Regex("""(\w+)\s*=\s*('((?:\\.|[^'\\])*)'|\S+)""")
            for (m in re.findAll(dsn)) {
                val quoted = m.groupValues[3]
                val value = if (m.groupValues[2].startsWith("'")) quoted.replace(Regex("""\\(.)"""), "$1") else m.groupValues[2]
                out[m.groupValues[1]] = value
            }
            return out
        }

        /** Starts the server with protocol [profile] ("strict" or "legacy") and waits for its health check. */
        fun start(profile: String): RealServer {
            val dsn = System.getenv("IMKIT_TEST_DSN")
            assumeTrue("set IMKIT_TEST_DSN to a throwaway PostgreSQL database", !dsn.isNullOrBlank())
            assumeTrue("build the server: cd server && go build -o bin/${binary.name} .", binary.isFile)
            val db = parseDsn(dsn!!)
            val port = ServerSocket(0).use { it.localPort }
            val secret = ByteArray(32).also { SecureRandom().nextBytes(it) }
            val configFile = File.createTempFile("imkit-standalone", ".json").apply { writeText("""{"relations":"allow-all"}""") }

            val pb = ProcessBuilder(binary.absolutePath).directory(serverDir).redirectErrorStream(true)
            pb.environment().apply {
                put("IM_PORT", port.toString())
                put("DB_HOST", db["host"] ?: "127.0.0.1")
                put("DB_PORT", db["port"] ?: "5432")
                put("DB_USER", db["user"] ?: "postgres")
                put("DB_PASSWORD", db["password"] ?: "")
                put("DB_NAME", db["dbname"] ?: error("IMKIT_TEST_DSN has no dbname"))
                put("DB_SSLMODE", db["sslmode"] ?: "disable")
                put("JWT_SECRET", secret.joinToString("") { "%02x".format(it) })
                put("IMKIT_HOST_MODE", "standalone")
                put("IMKIT_STANDALONE_CONFIG", configFile.absolutePath)
                put("IMKIT_PROTOCOL_PROFILE", profile)
                put("IM_CALL_DISCONNECT_GRACE_SEC", "1")
            }
            val process = pb.start()
            val log = StringBuffer()
            Thread {
                process.inputStream.bufferedReader().forEachLine { log.append(it).append('\n') }
            }.apply { isDaemon = true; start() }

            // The JWT secret is random per run; secret as a hex string is what the server verifies with.
            val server = RealServer(process, port, secret.joinToString("") { "%02x".format(it) }.toByteArray(), log, configFile)
            val http = OkHttpClient.Builder().connectTimeout(1, TimeUnit.SECONDS).readTimeout(2, TimeUnit.SECONDS).build()
            val deadline = System.currentTimeMillis() + 30_000
            while (true) {
                if (!process.isAlive) {
                    server.close()
                    error("imkit-server exited:\n${server.logTail()}")
                }
                val healthy = runCatching {
                    http.newCall(Request.Builder().url("${server.httpUrl}/api/health").build()).execute().use { it.code == 200 }
                }.getOrDefault(false)
                if (healthy) return server
                if (System.currentTimeMillis() > deadline) {
                    server.close()
                    error("imkit-server not healthy in 30s:\n${server.logTail()}")
                }
                Thread.sleep(100)
            }
        }
    }
}
