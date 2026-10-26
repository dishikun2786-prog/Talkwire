package io.talkwire.core

import io.talkwire.core.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

class ImIdsTest {
    @Test
    fun uidMatchesEveryContractVector() {
        val file = File(TestFiles.repoRoot, "contract/host-api/uid-vectors.json")
        val vectors = Json.parseObject(file.readText()).array("vectors")!!.objects()
        assertTrue("vectors present", vectors.size >= 9)
        for (v in vectors) {
            assertEquals(v.string("hostUserId"), v.string("uid"), ImIds.uid(v.string("hostUserId")!!))
        }
    }

    @Test
    fun p2pTopicMatchesTheSpecVectors() {
        // protocol spec 5.2
        val alice = ImIds.uid("alice")
        val bob = ImIds.uid("bob")
        assertEquals("p2p_E5DsdYCpVC8", ImIds.p2pTopic(alice, bob))
        assertEquals("p2p_E5DsdYCpVC8", ImIds.p2pTopic(bob, alice))
        assertEquals(
            "p2p_VVXxwJ61Kvw",
            ImIds.p2pTopic(ImIds.uid("00000000-0000-4000-8000-000000000001"), ImIds.uid("用户一")),
        )
    }

    @Test
    fun classifiesTopics() {
        assertTrue(ImIds.isUserUid("uid_K9gGyX8OAK8"))
        assertTrue(ImIds.isUserUid("usrAbc"))
        assertTrue(ImIds.isGroupTopic("grp_abcdefabcdef"))
        assertTrue(ImIds.isP2pTopic("p2p_E5DsdYCpVC8"))
    }
}

internal object TestFiles {
    val repoRoot: File = File(
        System.getProperty("talkwire.repoRoot")
            ?: error("talkwire.repoRoot is set by the Gradle test task"),
    )
}
