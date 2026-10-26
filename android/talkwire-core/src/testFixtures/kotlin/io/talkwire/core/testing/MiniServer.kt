package io.talkwire.core.testing

import io.talkwire.core.ImIds
import io.talkwire.core.json.JsonArray
import io.talkwire.core.json.JsonObject
import io.talkwire.core.json.JsonString
import io.talkwire.core.json.jsonObject
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList

/**
 * Just enough of the IM server, in memory, to test the chat engine: login
 * (token = appUserId), pub with clientMsgId de-duplication, get, del,
 * del_topic, note, sub, pres, get_groups, get_members, sync_summary (when
 * [capabilities] offer it), and the pushes they cause. Not a conformance
 * reference; the real server is tested by the golden transcripts.
 */
class MiniServer {
    data class Msg(
        val seq: Long,
        val from: String,
        val content: String,
        val contentType: String,
        val fileUrl: String?,
        val ts: Long,
        val clientMsgId: String?,
        var recalled: Boolean = false,
    )

    class Group(val name: String, val members: MutableMap<String, String>)

    val transport = FakeServer()
    private val topics = ConcurrentHashMap<String, MutableList<Msg>>()
    private val clears = ConcurrentHashMap<String, Pair<Long, Long>>()
    val groups = ConcurrentHashMap<String, Group>()
    private val users = ConcurrentHashMap<FakeServer.Conn, String>()
    private var clock = 1_000L

    /** The login reply's `capabilities`; add "sync-summary" to offer `sync_summary` (spec 8.1). */
    var capabilities: List<String> = emptyList()

    /** Every uid seen, to name the peer of a `p2p_…` topic in `sync_summary`. */
    private val knownUids = ConcurrentHashMap.newKeySet<String>()

    /** Return true to swallow the reply to this request. */
    var dropReply: (JsonObject) -> Boolean = { false }

    /** Return true to lose this request entirely: not handled, not answered. */
    var loseRequest: (JsonObject) -> Boolean = { false }

    /** Return a non-null reply to answer instead of the normal handling. */
    var override: (JsonObject) -> JsonObject? = { null }

    /**
     * Like [override], with the sender's uid: for module actions a test plays
     * the server's side of (it may [pushTo] others before answering).
     */
    var overrideAs: (me: String, request: JsonObject) -> JsonObject? = { _, _ -> null }

    val requests = CopyOnWriteArrayList<JsonObject>()

    /**
     * Keep each user's `read` notes as their read position, the way a strict
     * server does (spec 12.3): a direct chat's get reply then carries the
     * caller's own `readSeq`, a group's carries `readCursors` (as a server
     * with read receipts on does, legacy or strict).
     */
    var storeReads: Boolean = false
    private val reads = ConcurrentHashMap<Pair<String, String>, Long>()
    private val readTimes = ConcurrentHashMap<Pair<String, String>, Long>()

    /** Sets [uid]'s stored read position in [topic] (canonical), as if one of their devices had read it. */
    fun setRead(topic: String, uid: String, seq: Long) {
        reads[topic to uid] = seq
        readTimes[topic to uid] = ++clock
    }

    fun readOf(topic: String, uid: String): Long? = reads[topic to uid]

    init {
        transport.onFrame = { c, f -> handle(c, f) }
    }

    fun uidOf(appUserId: String) = ImIds.uid(appUserId)

    fun canonical(me: String, topic: String): String =
        if (ImIds.isUserUid(topic)) ImIds.p2pTopic(me, topic) else topic

    fun messages(topic: String): List<Msg> = topics[topic]?.toList() ?: emptyList()

    /** Stores a message as if [fromUid] sent it while nobody watched. */
    fun inject(topic: String, fromUid: String, content: String, fileUrl: String? = null, contentType: String = "text"): Long {
        knownUids += fromUid
        val list = topics.getOrPut(topic) { CopyOnWriteArrayList() }
        val seq = (list.maxOfOrNull { it.seq } ?: clears[topic]?.second ?: 0) + 1
        list += Msg(seq, fromUid, content, contentType, fileUrl, ++clock, null)
        return seq
    }

    fun recallSilently(topic: String, seq: Long) {
        topics[topic]?.firstOrNull { it.seq == seq }?.recalled = true
    }

    private fun online(uid: String) = users.filterValues { it == uid }.keys.filter { it.open }

    private fun reply(c: FakeServer.Conn, req: JsonObject, body: JsonObject) {
        if (dropReply(req)) return
        val id = req.string("id")
        c.push(if (id != null) body.with("id", JsonString(id)) else body)
    }

    private fun handle(c: FakeServer.Conn, f: JsonObject) {
        requests += f
        val action = f.string("action") ?: return
        if (action == "login") {
            val uid = uidOf(f.string("token") ?: "")
            users[c] = uid
            knownUids += uid
            c.push(
                jsonObject {
                    put("id", f.string("id")); put("action", "login"); put("code", 200); put("uid", uid); put("protocol", 1)
                    if (capabilities.isNotEmpty()) putStrings("capabilities", capabilities)
                },
            )
            return
        }
        val me = users[c] ?: return
        if (loseRequest(f)) return
        override(f)?.let { reply(c, f, it); return }
        overrideAs(me, f)?.let { reply(c, f, it); return }
        val topic = f.string("topic") ?: ""
        when (action) {
            "pub" -> {
                if (ImIds.isUserUid(topic)) knownUids += topic
                val canon = canonical(me, topic)
                val list = topics.getOrPut(canon) { CopyOnWriteArrayList() }
                val cid = f.string("clientMsgId")
                val dup = cid?.let { id -> list.firstOrNull { it.clientMsgId == id && it.from == me } }
                if (dup != null) {
                    reply(c, f, jsonObject { put("action", "pub"); put("code", 200); put("topic", topic); put("seq", dup.seq); put("ts", dup.ts) })
                    return
                }
                val seq = (list.maxOfOrNull { it.seq } ?: clears[canon]?.second ?: 0) + 1
                val m = Msg(seq, me, f.string("content") ?: "", f.string("contentType") ?: "", f.string("fileUrl"), ++clock, cid)
                list += m
                reply(c, f, jsonObject { put("action", "pub"); put("code", 200); put("topic", topic); put("seq", seq); put("ts", m.ts) })
                for (uid in membersOf(me, topic) - me) {
                    for (o in online(uid)) {
                        o.push(
                            jsonObject {
                                put("action", "data")
                                put("topic", if (ImIds.isGroupTopic(topic)) topic else me)
                                put("from", me)
                                put("seq", seq)
                                put("ts", m.ts)
                                put("content", m.content)
                                put("contentType", m.contentType)
                                put("fileUrl", m.fileUrl)
                                put("clientMsgId", cid)
                                if (ImIds.isGroupTopic(topic)) put("name", groups[topic]?.name)
                            },
                        )
                    }
                }
            }
            "get" -> {
                val canon = canonical(me, topic)
                val all = messages(canon).filter { !it.recalled }.sortedBy { it.seq }
                val limit = f.int("limit")?.takeIf { it in 1..200 } ?: 50
                val after = f.long("afterSeq")
                val before = f.long("beforeSeq")
                val page = when {
                    after != null -> all.filter { it.seq > after }.take(limit)
                    before != null -> all.filter { it.seq < before }.takeLast(limit)
                    else -> all.takeLast(limit)
                }
                reply(
                    c,
                    f,
                    jsonObject {
                        put("action", "get")
                        put("code", 200)
                        put("topic", topic)
                        if (page.isNotEmpty()) {
                            put(
                                "messages",
                                JsonArray(
                                    page.map { m ->
                                        jsonObject {
                                            put("topic", canon)
                                            put("seq", m.seq)
                                            put("from", m.from)
                                            put("content", m.content)
                                            put("contentType", m.contentType)
                                            put("fileUrl", m.fileUrl)
                                            put("ts", m.ts)
                                        }
                                    },
                                ),
                            )
                        }
                        clears[canon]?.let { (v, b) -> put("clearVersion", v); put("clearBeforeSeq", b) }
                        if (storeReads && ImIds.isGroupTopic(canon)) {
                            val cursors = reads.filterKeys { it.first == canon }
                            if (cursors.isNotEmpty()) {
                                put("readCursors", JsonArray(cursors.map { (k, v) -> jsonObject { put("uid", k.second); put("seq", v) } }))
                            }
                        } else if (storeReads) {
                            put("readSeq", reads[canon to me])
                        }
                    },
                )
            }
            "del" -> {
                val canon = canonical(me, topic)
                val m = messages(canon).firstOrNull { it.seq == f.long("seq") && it.from == me && !it.recalled }
                if (m == null) {
                    reply(c, f, jsonObject { put("action", "del"); put("code", 404); put("reason", "message not found or not yours") })
                    return
                }
                m.recalled = true
                reply(c, f, jsonObject { put("action", "del"); put("code", 200); put("topic", topic); put("seq", m.seq) })
                for (uid in membersOf(me, topic) - me) for (o in online(uid)) {
                    o.push(jsonObject { put("action", "recalled"); put("topic", topic); put("seq", m.seq); put("from", me) })
                }
            }
            "del_topic" -> {
                val canon = canonical(me, topic)
                val last = messages(canon).maxOfOrNull { it.seq } ?: clears[canon]?.second ?: 0
                val version = (clears[canon]?.first ?: 0) + 1
                clears[canon] = version to last
                topics[canon]?.clear()
                reply(c, f, jsonObject { put("action", "del_topic"); put("code", 200); put("topic", topic); put("clearVersion", version); put("clearBeforeSeq", last) })
                for (uid in membersOf(me, topic) - me) for (o in online(uid)) {
                    o.push(
                        jsonObject {
                            put("action", "topic_cleared"); put("topic", if (ImIds.isGroupTopic(topic)) topic else me)
                            put("from", me); put("clearVersion", version); put("clearBeforeSeq", last)
                        },
                    )
                }
            }
            "note" -> {
                val seq = f.long("seq")
                if (storeReads && f.string("what") == "read" && seq != null && seq > 0) {
                    val canon = canonical(me, topic)
                    val last = messages(canon).maxOfOrNull { it.seq } ?: 0
                    val before = reads[canon to me]
                    reads.merge(canon to me, minOf(seq, last)) { a, b -> maxOf(a, b) }
                    if (reads[canon to me] != before) readTimes[canon to me] = ++clock
                }
                for (uid in membersOf(me, topic) - me) for (o in online(uid)) {
                    o.push(jsonObject { put("action", "info"); put("topic", topic); put("from", me); put("what", f.string("what")); put("seq", seq) })
                }
            }
            "sub" -> reply(c, f, jsonObject { put("action", "sub"); put("code", 200); put("topic", topic) })
            "pres" -> reply(c, f, jsonObject { put("action", "pres"); put("code", 200); put("what", "snapshot"); put("states", JsonArray(emptyList())) })
            "get_groups" -> reply(
                c,
                f,
                jsonObject {
                    put("action", "get_groups")
                    put("code", 200)
                    val mine = groups.filter { me in it.value.members }
                    if (mine.isNotEmpty()) {
                        put(
                            "groups",
                            JsonArray(
                                mine.map { (t, g) ->
                                    jsonObject {
                                        put("topic", t); put("name", g.name)
                                        put("owner", g.members.entries.first { it.value == "owner" }.key); put("members", g.members.size)
                                    }
                                },
                            ),
                        )
                    }
                },
            )
            "get_members" -> {
                val g = groups[topic]
                if (g == null || me !in g.members) {
                    reply(c, f, jsonObject { put("action", "get_members"); put("code", 401); put("reason", "not a member") })
                    return
                }
                reply(
                    c,
                    f,
                    jsonObject {
                        put("action", "get_members"); put("code", 200); put("topic", topic)
                        put("members", JsonArray(g.members.map { (uid, role) -> jsonObject { put("uid", uid); put("role", role); put("joinedAt", 1) } }))
                    },
                )
            }
            "sync_summary" -> if (SYNC_SUMMARY !in capabilities) {
                reply(c, f, jsonObject { put("action", action); put("code", 400); put("reason", "unknown action: $action") })
            } else {
                val mine = sortedMapOf<String, JsonObject>()
                for ((t, g) in groups) if (me in g.members) mine[t] = summaryOf(me, t, peer = null)
                for (t in topics.keys + clears.keys) {
                    if (!ImIds.isP2pTopic(t)) continue
                    val peer = knownUids.firstOrNull { it != me && ImIds.p2pTopic(me, it) == t } ?: continue
                    mine[t] = summaryOf(me, t, peer)
                }
                reply(
                    c,
                    f,
                    jsonObject {
                        put("action", "sync_summary"); put("code", 200)
                        if (mine.isNotEmpty()) put("topics", JsonArray(mine.values.toList()))
                    },
                )
            }
            else -> reply(c, f, jsonObject { put("action", action); put("code", 400); put("reason", "unknown action: $action") })
        }
    }

    /** One `sync_summary` entry (spec 8.1); [peer] is null for a group. */
    private fun summaryOf(me: String, topic: String, peer: String?): JsonObject = jsonObject {
        put("topic", topic)
        put("lastSeq", messages(topic).maxOfOrNull { it.seq } ?: clears[topic]?.second ?: 0)
        put("isGroup", peer == null)
        clears[topic]?.let { (v, b) -> put("clearVersion", v); put("clearBeforeSeq", b) }
        put("peer", peer)
        if (storeReads) {
            put("readSeq", reads[topic to me])
            if (peer == null) put("readsAt", readTimes.filterKeys { it.first == topic && it.second != me }.values.maxOrNull())
        }
    }

    private fun membersOf(me: String, topic: String): Set<String> =
        if (ImIds.isGroupTopic(topic)) groups[topic]?.members?.keys ?: emptySet() else setOf(me, topic)

    /** Pushes a frame to every online connection of [uid]. */
    fun pushTo(uid: String, frame: JsonObject) {
        for (o in online(uid)) o.push(frame)
    }

    fun dropAll(uid: String) {
        for (o in online(uid)) o.drop()
    }

    companion object {
        const val SYNC_SUMMARY: String = "sync-summary"
    }
}
