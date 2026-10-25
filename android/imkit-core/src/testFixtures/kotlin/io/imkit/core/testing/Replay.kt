package io.imkit.core.testing

import io.imkit.core.json.Json
import io.imkit.core.json.JsonObject
import io.imkit.core.transport.ImSocket
import io.imkit.core.transport.ImSocketListener
import io.imkit.core.transport.ImTransport
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Assume.assumeTrue
import java.io.File
import java.net.ServerSocket
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * One run of `tools/conformance/bin/replay`: it plays the server's side of a
 * golden transcript to the client under test and exits 0 only if the client
 * sent exactly the scripted requests. Tests are skipped when the binary has
 * not been built (`cd tools/conformance && go build -o bin/replay.exe ./cmd/replay`).
 *
 * The test task must set the system property `imkit.repoRoot` to the
 * repository root, where the scenarios, transcripts and binary are.
 */
class Replay private constructor(
    private val process: Process,
    val port: Int,
    private val log: StringBuffer,
    private val reader: Thread,
    private val faultsFile: File?,
) : AutoCloseable {
    /** What the replay printed so far. */
    val output: String get() = log.toString()

    /** The replay accepts any path. */
    val url: String get() = "ws://127.0.0.1:$port/"

    /** Waits for the replay to finish and fails the test unless the client followed the script. */
    fun assertPassed(timeoutMs: Long = 30_000) {
        if (!process.waitFor(timeoutMs, TimeUnit.MILLISECONDS)) {
            process.destroyForcibly()
            fail("replay did not finish in time:\n$output")
        }
        reader.join(5_000)
        assertEquals("replay output:\n$output", 0, process.exitValue())
        assertTrue("replay output:\n$output", output.contains("PASS"))
    }

    override fun close() {
        if (process.isAlive) process.destroyForcibly()
        faultsFile?.delete()
    }

    companion object {
        private val repoRoot = File(System.getProperty("imkit.repoRoot") ?: error("set the system property imkit.repoRoot in the test task"))
        private val conformance = File(repoRoot, "tools/conformance")
        private val scenarios = File(repoRoot, "contract/protocol/scenarios")
        private val transcripts = File(repoRoot, "contract/protocol/transcripts")
        private val binary = File(
            conformance,
            if (System.getProperty("os.name").startsWith("Windows")) "bin/replay.exe" else "bin/replay",
        )

        private fun requireBinary() =
            assumeTrue("replay binary not built: cd tools/conformance && go build -o bin/${binary.name} ./cmd/replay", binary.isFile)

        private fun baseArgs(scenario: String) = listOf(
            binary.absolutePath,
            "-scenarios", scenarios.absolutePath,
            "-transcripts", transcripts.absolutePath,
            "-scenario", scenario,
        )

        /** The concrete value of every placeholder of [scenario], keyed like "uid:A". */
        fun bindings(scenario: String): Map<String, String> {
            requireBinary()
            val p = ProcessBuilder(baseArgs(scenario) + "-bindings").directory(conformance).redirectErrorStream(true).start()
            val text = p.inputStream.bufferedReader().readText()
            check(p.waitFor(10, TimeUnit.SECONDS) && p.exitValue() == 0) { "replay -bindings failed:\n$text" }
            return text.lines().mapNotNull { line ->
                val m = Regex("""^\$\{(.+)} = (.*)$""").find(line.trim()) ?: return@mapNotNull null
                m.groupValues[1] to m.groupValues[2]
            }.toMap()
        }

        /** Starts replaying [scenario] as scenario user [user]; returns once it listens. */
        fun start(scenario: String, user: String, faults: String? = null, vararg extra: String): Replay {
            requireBinary()
            val port = ServerSocket(0).use { it.localPort }
            val faultsFile = faults?.let { f -> File.createTempFile("replay-faults", ".json").also { it.writeText(f) } }
            val args = baseArgs(scenario) + listOf("-user", user, "-addr", "127.0.0.1:$port") +
                (faultsFile?.let { listOf("-faults", "@" + it.absolutePath) } ?: emptyList()) + extra
            val process = ProcessBuilder(args).directory(conformance).redirectErrorStream(true).start()
            val output = StringBuffer()
            val listening = CountDownLatch(1)
            val reader = Thread {
                process.inputStream.bufferedReader().forEachLine { line ->
                    output.append(line).append('\n')
                    if (line.startsWith("replaying ")) listening.countDown()
                }
                listening.countDown()
            }.apply { isDaemon = true; start() }
            if (!listening.await(10, TimeUnit.SECONDS) || !output.contains("replaying ")) {
                process.destroyForcibly()
                fail("replay did not start:\n$output")
            }
            return Replay(process, port, output, reader, faultsFile)
        }
    }
}

/** Passes everything through and keeps a copy of every frame the client sends. */
class RecordingTransport(private val delegate: ImTransport) : ImTransport {
    val sent = CopyOnWriteArrayList<JsonObject>()

    fun sentActions(action: String): List<JsonObject> = sent.filter { it.string("action") == action }

    override fun open(url: String, listener: ImSocketListener): ImSocket {
        val socket = delegate.open(url, listener)
        return object : ImSocket {
            override fun send(text: String): Boolean {
                runCatching { sent += Json.parseObject(text) }
                return socket.send(text)
            }

            override fun close(code: Int, reason: String) = socket.close(code, reason)

            override fun cancel() = socket.cancel()
        }
    }
}
