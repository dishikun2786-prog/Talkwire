package io.imkit.groupjoin

import io.imkit.core.client.ImClient
import io.imkit.core.client.ImClientConfig
import io.imkit.core.client.ImException
import io.imkit.core.client.TokenProvider
import io.imkit.core.client.members
import io.imkit.core.testing.Replay
import io.imkit.core.transport.OkHttpTransport
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList

/**
 * The scan-to-join client against the golden transcripts `module/qr-join`
 * and `module/qr-join-review`: the replay plays the server and fails unless
 * the client sends exactly the scripted requests. Each test is one scenario
 * user; the step numbers are the transcript's. Skipped when the replay binary
 * is not built.
 */
class GroupJoinReplayTest {
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val clients = CopyOnWriteArrayList<ImClient>()

    @After
    fun tearDown() {
        clients.forEach { it.stop() }
        scope.cancel()
    }

    private suspend fun connect(replay: Replay, user: String): Pair<ImClient, GroupJoinClient> {
        val client = ImClient(
            ImClientConfig(
                endpoints = listOf(replay.url),
                tokenProvider = TokenProvider { "token-of-$user" }, // the replay does not check tokens
                transport = OkHttpTransport(),
                requestTimeoutMs = 5_000,
            ),
            scope,
        ).also { clients += it }
        client.start()
        assertNotNull("not connected", client.awaitConnected(10_000))
        return client to GroupJoinClient(client)
    }

    /** Collects [api]'s events from now on. */
    private fun record(api: GroupJoinClient): MutableList<GroupJoinEvent> {
        val seen = CopyOnWriteArrayList<GroupJoinEvent>()
        scope.launch(start = CoroutineStart.UNDISPATCHED) { api.events.collect { seen += it } }
        return seen
    }

    private suspend fun until(what: String, check: () -> Boolean) {
        try {
            withTimeout(10_000) { while (!check()) delay(10) }
        } catch (e: kotlinx.coroutines.TimeoutCancellationException) {
            throw AssertionError("timed out waiting for $what")
        }
    }

    private suspend fun refused(code: Int, block: suspend () -> Unit) {
        try {
            block()
        } catch (e: ImException.Refused) {
            assertEquals(code, e.reply.code)
            return
        }
        fail("expected a refusal with $code")
    }

    private fun replaying(scenario: String, user: String, block: suspend (Replay) -> Unit) = runBlocking {
        Replay.start(scenario, user).use { replay ->
            try {
                block(replay)
            } catch (e: Throwable) {
                throw AssertionError("$e\n--- replay output so far ---\n${replay.output}", e)
            }
            replay.assertPassed()
        }
    }

    @Test
    fun aNewcomerPreviewsJoinsAndJoinsAgain() {
        val b = Replay.bindings("module/qr-join")
        val grp = b.getValue("grp")
        replaying("module/qr-join", "D") { replay ->
            val (client, api) = connect(replay, "D")
            refused(401) { api.qrToken(grp) } // step 11: not a member
            assertNull(api.resolve(b.getValue("qr1"))) // 15: replaced by the refresh
            assertEquals(QrPreview(grp, "scan", 2, alreadyMember = false, isFull = false, requireApproval = false), api.resolve(b.getValue("qr2"))) // 16
            assertNull(api.resolve("no-such-token")) // 17
            assertEquals(JoinResult(grp, JoinStatus.JOINED), api.join(b.getValue("qr2"))) // 18
            assertEquals(JoinResult(grp, JoinStatus.JOINED), api.join(b.getValue("qr2"))) // 19: already a member
            assertTrue(client.members(grp).any { it.uid == b.getValue("uid:D") }) // 20
            assertTrue(api.requireApproval(grp)) // 24
            refused(401) { api.requests(grp) } // 27: a plain member
        }
    }

    @Test
    fun anAdminSharesRefreshesAndReviews() {
        val b = Replay.bindings("module/qr-join")
        val grp = b.getValue("grp")
        replaying("module/qr-join", "B") { replay ->
            val (_, api) = connect(replay, "B")
            val seen = record(api)
            // Steps 9 and 10 make B a member and an admin; the replay paces itself.
            assertEquals(b.getValue("qr1"), api.qrToken(grp).qrToken) // 13: the same current token
            val fresh = api.qrToken(grp, refresh = true) // 14
            assertEquals(b.getValue("qr2"), fresh.qrToken)
            assertTrue("expiresAt is epoch milliseconds", fresh.expiresAt > 1_577_836_800_000L)
            refused(401) { api.setRequireApproval(grp, true) } // 22: the owner only
            until("the request reaches the admins") { seen.size == 1 } // 25
            assertEquals(GroupJoinEvent.Request(grp, "scan", b.getValue("uid:E")), seen[0])
            val pending = api.requests(grp) // 28
            assertEquals(1, pending.size)
            assertEquals(1L, pending[0].id)
            assertEquals(b.getValue("uid:E"), pending[0].requesterUid)
            assertEquals("please let me in", pending[0].message)
            assertTrue(api.review(grp, pending[0].id, approve = true)) // 29
            assertEquals(emptyList<JoinRequest>(), api.requests(grp)) // 31: nothing pending, the field omitted
        }
    }

    @Test
    fun anApplicantAsksTwiceAndHearsTheAnswer() {
        val b = Replay.bindings("module/qr-join")
        val grp = b.getValue("grp")
        replaying("module/qr-join", "E") { replay ->
            val (_, api) = connect(replay, "E")
            val seen = record(api)
            refused(401) { api.requireApproval(grp) } // 21: not a member
            assertEquals(JoinResult(grp, JoinStatus.PENDING_APPROVAL), api.join(b.getValue("qr2"), "please let me in")) // 25
            assertEquals(JoinResult(grp, JoinStatus.PENDING_APPROVAL), api.join(b.getValue("qr2"), "again")) // 26: no second notice
            until("the review result") { seen.size == 1 } // 29
            assertEquals(GroupJoinEvent.Reviewed(grp, "scan", approved = true), seen[0])
        }
    }

    @Test
    fun aRejectedApplicantAsksAgain() {
        val b = Replay.bindings("module/qr-join-review")
        val g2 = b.getValue("g2")
        replaying("module/qr-join-review", "E") { replay ->
            val (client, api) = connect(replay, "E")
            val seen = record(api)
            assertEquals(JoinStatus.PENDING_APPROVAL, api.join(b.getValue("qr"), "first try").status) // 13
            until("rejected") { seen.size == 1 } // 15
            val reviewed = seen[0] as GroupJoinEvent.Reviewed
            assertEquals(g2, reviewed.topic)
            assertFalse(reviewed.approved)
            refused(401) { client.members(g2) } // 16: not a member
            assertEquals(JoinStatus.PENDING_APPROVAL, api.join(b.getValue("qr"), "second try").status) // 18: the same token works again (F56)
            runCatching { client.members(g2) } // 22: on the legacy server E never joins (D8)
        }
    }
}
