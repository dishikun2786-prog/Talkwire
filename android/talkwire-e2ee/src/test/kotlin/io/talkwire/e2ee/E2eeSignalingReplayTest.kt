// SPDX-License-Identifier: AGPL-3.0-only
package io.talkwire.e2ee

import io.talkwire.core.client.ImClient
import io.talkwire.core.client.ImClientConfig
import io.talkwire.core.client.TokenProvider
import io.talkwire.core.json.JsonArray
import io.talkwire.core.json.JsonObject
import io.talkwire.core.json.JsonString
import io.talkwire.core.json.jsonObject
import io.talkwire.core.testing.Replay
import io.talkwire.core.transport.OkHttpTransport
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
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList

/**
 * The protocol layer against the golden transcripts `module/e2ee-p2p` and
 * `module/e2ee-group`: the replay plays the server and fails unless the
 * client sends exactly the scripted requests. The payloads are the
 * transcripts' dummy base64, so no libsignal here. Requests the module never
 * sends (a bundle without its identity key, plain group actions) go through
 * the client directly. Step numbers are the transcripts'. Skipped when the
 * replay binary is not built.
 */
class E2eeSignalingReplayTest {
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val clients = CopyOnWriteArrayList<ImClient>()

    @After
    fun tearDown() {
        clients.forEach { it.stop() }
        scope.cancel()
    }

    private class Party(val signaling: E2eeSignaling, val events: CopyOnWriteArrayList<E2eeEvent>) {
        val client get() = signaling.client
    }

    private suspend fun connect(replay: Replay, user: String): Party {
        val client = ImClient(
            ImClientConfig(
                endpoints = listOf(replay.url),
                tokenProvider = TokenProvider { "token-of-$user" },
                transport = OkHttpTransport(),
                requestTimeoutMs = 10_000,
                reconnectBaseDelayMs = 50,
                reconnectMaxDelayMs = 200,
            ),
            scope,
        ).also { clients += it }
        val signaling = E2eeSignaling(client)
        val events = CopyOnWriteArrayList<E2eeEvent>()
        scope.launch(start = CoroutineStart.UNDISPATCHED) { signaling.events.collect { events += it } }
        client.start()
        assertNotNull("not connected", client.awaitConnected(10_000))
        return Party(signaling, events)
    }

    private suspend fun until(what: String, check: () -> Boolean) {
        try {
            withTimeout(15_000) { while (!check()) delay(10) }
        } catch (e: kotlinx.coroutines.TimeoutCancellationException) {
            throw AssertionError("timed out waiting for $what")
        }
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

    private val bundleA = WireBundle(11, "SUtB", 1, "U1BLQQ", "U0lHQQ", 1, "S1BLQQ", "S1NJR0E")

    @Test
    fun directAsTheOneWhoPublishes() {
        val b = Replay.bindings("module/e2ee-p2p")
        val topic = b.getValue("p2p:A,B")
        val uidB = b.getValue("uid:B")
        replaying("module/e2ee-p2p", "A") { replay ->
            val a = connect(replay, "A")
            val batch = listOf(WireOneTimeKey(101, "T1RQMTAx"), WireOneTimeKey(102, "T1RQMTAy"))
            assertEquals(E2eeResult.Ok(Unit), a.signaling.publish(bundleA.copy(oneTimePreKeys = batch))) // 7
            val noIdentity = a.client.requestTyped(
                jsonObject {
                    put("action", "e2e_publish_bundle")
                    put(
                        "e2eBundle",
                        jsonObject {
                            put("registrationId", 11); put("signedPreKeyId", 1); put("signedPreKey", "U1BLQQ")
                            put("signedPreKeySignature", "U0lHQQ"); put("kyberPreKeyId", 1); put("kyberPreKey", "S1BLQQ")
                            put("kyberPreKeySignature", "S1NJR0E")
                        },
                    )
                },
            ) // 8
            assertEquals("bad_request", noIdentity.string("reason"))
            assertEquals(E2eeResult.Ok(2), a.signaling.oneTimeKeysLeft()) // 9
            assertEquals(E2eeResult.Ok(0), a.signaling.oneTimeKeysLeft()) // 13
            assertEquals(E2eeResult.Failed(E2eeFailure.PEER_KEYS_UNAVAILABLE), a.signaling.fetch(uidB)) // 15

            until("the live envelope") { a.events.isNotEmpty() } // 16
            assertEquals(E2eeEvent.Deliver(topic, uidB, "msg", 3, "Q0lQSEVSMQ", (a.events[0] as E2eeEvent.Deliver).ts), a.events[0])
            a.client.stop() // 19: offline; B's envelope is queued (20)
            a.client.start() // 21, 22: the queued envelope comes with the login
            until("the queued envelope") { a.events.size >= 2 }
            val queued = a.events[1] as E2eeEvent.Deliver
            assertEquals(listOf("msg", "2", "Q0lQSEVSMg"), listOf(queued.kind, queued.msgType.toString(), queued.content))
            a.client.stop() // 23
            a.client.start() // 24, 25: nothing again
            until("clear and terminate") { a.events.size >= 4 } // 26, 27
            assertEquals(E2eeEvent.Cleared(topic, uidB), a.events[2])
            assertEquals(E2eeEvent.Terminated(topic, uidB), a.events[3])
        }
    }

    @Test
    fun directAsTheOneWhoSends() {
        val b = Replay.bindings("module/e2ee-p2p")
        val topic = b.getValue("p2p:A,B")
        val uidA = b.getValue("uid:A")
        replaying("module/e2ee-p2p", "B") { replay ->
            val p = connect(replay, "B")
            val first = (p.signaling.fetch(uidA) as E2eeResult.Ok).value // 10
            assertEquals(bundleA.copy(oneTimePreKeys = listOf(WireOneTimeKey(101, "T1RQMTAx"))), first)
            assertEquals(listOf(WireOneTimeKey(102, "T1RQMTAy")), (p.signaling.fetch(uidA) as E2eeResult.Ok).value.oneTimePreKeys) // 11
            assertEquals(bundleA, (p.signaling.fetch(uidA) as E2eeResult.Ok).value) // 12: none left
            assertEquals(E2eeResult.Ok(Unit), p.signaling.send(topic, "msg", "Q0lQSEVSMQ", 3, uidA)) // 16
            assertEquals(E2eeFailure.SERVER, (p.signaling.send(topic, "msg", null, 2, uidA) as E2eeResult.Failed).failure) // 18
            assertEquals(E2eeResult.Ok(Unit), p.signaling.send(topic, "msg", "Q0lQSEVSMg", 2, uidA)) // 20: queued
            assertEquals(E2eeResult.Ok(Unit), p.signaling.clear(topic, uidA)) // 26
            assertEquals(E2eeResult.Ok(Unit), p.signaling.terminate(topic, uidA)) // 27
        }
    }

    @Test
    fun directAsAStranger() {
        val b = Replay.bindings("module/e2ee-p2p")
        val uidA = b.getValue("uid:A")
        replaying("module/e2ee-p2p", "C") { replay ->
            val c = connect(replay, "C")
            assertEquals(E2eeResult.Failed(E2eeFailure.PEER_KEYS_UNAVAILABLE), c.signaling.fetch(uidA)) // 14
            val refused = c.signaling.send(b.getValue("p2p:A,C"), "msg", "Q0lQSEVSWA", 3, uidA) as E2eeResult.Failed // 17
            assertEquals(E2eeFailure.NOT_ALLOWED, refused.failure)
        }
    }

    @Test
    fun groupAsTheOwner() {
        val b = Replay.bindings("module/e2ee-group")
        val grp = b.getValue("grp")
        replaying("module/e2ee-group", "A") { replay ->
            val a = connect(replay, "A")
            val created = a.client.request(
                jsonObject {
                    put("action", "create_grp"); put("name", "secret")
                    put("members", JsonArray(listOf(JsonString(b.getValue("uid:B")), JsonString(b.getValue("uid:C")))))
                },
            ) // 7
            assertEquals(201, created.code)
            assertEquals(E2eeResult.Failed(E2eeFailure.GROUP_NOT_ENCRYPTED, null), (a.signaling.send(grp, "sender_key", "U0tE") as E2eeResult.Failed).copy(detail = null)) // 10
            assertEquals(E2eeResult.Ok(Unit), a.signaling.setGroupEncrypted(grp)) // 12
            assertEquals(E2eeResult.Ok(Unit), a.signaling.setGroupEncrypted(grp)) // 13: again
            val plain = a.client.request(jsonObject { put("action", "pub"); put("topic", grp); put("content", "plain"); put("contentType", "text/plain"); put("clientMsgId", "a-plain") }) // 15
            assertEquals(409 to "group_encrypted", plain.code to plain.reason)
            val groups = a.client.request(jsonObject { put("action", "get_groups") }) // 16
            assertEquals(true, (groups.frame.array("groups")!!.items[0] as JsonObject).bool("encrypted"))
            until("bob's key and message") { a.events.count { it is E2eeEvent.Deliver } >= 2 } // 17, 18
            assertEquals(listOf("sender_key", "msg"), a.events.filterIsInstance<E2eeEvent.Deliver>().map { it.kind })
            assertEquals(E2eeResult.Ok(Unit), a.signaling.groupClear(grp)) // 20
            assertEquals(200, a.client.request(jsonObject { put("action", "kick"); put("topic", grp); put("user", b.getValue("uid:C")) }).code) // 21
            until("bob leaving") { a.events.any { it is E2eeEvent.GroupEncrypted && it.reason == "member_left" } } // 22
            val reasons = a.events.filterIsInstance<E2eeEvent.GroupEncrypted>().map { it.reason }
            assertEquals(listOf("owner_enabled", "owner_enabled", "member_removed", "member_left"), reasons)
        }
    }

    @Test
    fun groupAsAMember() {
        val b = Replay.bindings("module/e2ee-group")
        val grp = b.getValue("grp")
        val uidA = b.getValue("uid:A")
        val uidC = b.getValue("uid:C")
        replaying("module/e2ee-group", "B") { replay ->
            val p = connect(replay, "B")
            assertEquals(E2eeResult.Failed(E2eeFailure.PEER_KEYS_UNAVAILABLE), p.signaling.fetch(uidC)) // 9
            assertEquals(E2eeFailure.NOT_ALLOWED, (p.signaling.setGroupEncrypted(grp) as E2eeResult.Failed).failure) // 11
            until("encryption on, twice") { p.events.count { it is E2eeEvent.GroupEncrypted } >= 2 } // 12, 13
            assertEquals(E2eeEvent.GroupEncrypted(grp, uidA, "owner_enabled"), p.events.first { it is E2eeEvent.GroupEncrypted })
            assertEquals(WireBundle(33, "SUtD", 1, "U1BLQw", "U0lHQw", 1, "S1BLQw", "S1NJR0M"), (p.signaling.fetch(uidC) as E2eeResult.Ok).value) // 14
            assertEquals(E2eeResult.Ok(Unit), p.signaling.send(grp, "sender_key", "U0tEQg", 7)) // 17
            assertEquals(E2eeResult.Ok(Unit), p.signaling.send(grp, "msg", "R0NJUEhFUg", 7)) // 18
            assertEquals(E2eeFailure.NOT_ALLOWED, (p.signaling.groupClear(grp) as E2eeResult.Failed).failure) // 19
            until("the owner's clear and the removal") { p.events.any { it is E2eeEvent.GroupEncrypted && it.reason == "member_removed" } } // 20, 21
            assertTrue(p.events.contains(E2eeEvent.GroupCleared(grp, uidA)))
            assertEquals(200, p.client.request(jsonObject { put("action", "leave_grp"); put("topic", grp) }).code) // 22
        }
    }

    @Test
    fun groupAsTheMemberWhoIsRemoved() {
        val b = Replay.bindings("module/e2ee-group")
        val grp = b.getValue("grp")
        val uidB = b.getValue("uid:B")
        replaying("module/e2ee-group", "C") { replay ->
            val c = connect(replay, "C")
            assertEquals(E2eeResult.Ok(Unit), c.signaling.publish(WireBundle(33, "SUtD", 1, "U1BLQw", "U0lHQw", 1, "S1BLQw", "S1NJR0M"))) // 8
            until("everything up to the clear") { c.events.any { it is E2eeEvent.GroupCleared } } // 12 to 20
            assertEquals(
                listOf(E2eeEvent.Deliver(grp, uidB, "sender_key", 7, "U0tEQg", 0), E2eeEvent.Deliver(grp, uidB, "msg", 7, "R0NJUEhFUg", 0)),
                c.events.filterIsInstance<E2eeEvent.Deliver>().map { it.copy(ts = 0) },
            )
            assertEquals(2, c.events.count { it is E2eeEvent.GroupEncrypted })
        }
    }
}
