package io.imkit.core.json

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class JsonTest {
    @Test
    fun roundTripsServerFrames() {
        val text = """{"action":"data","topic":"grp_abc","seq":3,"ts":1792843714123,"content":"你好\n\"引号\"","extra":{"a":[1,2.5,true,null]}}"""
        val v = Json.parseObject(text)
        assertEquals("data", v.string("action"))
        assertEquals(1792843714123L, v.long("ts"))
        assertEquals(3, v.int("seq"))
        assertEquals("你好\n\"引号\"", v.string("content"))
        assertEquals(text, Json.stringify(v))
    }

    @Test
    fun absentNullAndWrongTypeAllReadAsNull() {
        val v = Json.parseObject("""{"a":null,"b":"x","c":1}""")
        assertNull(v.string("a"))
        assertNull(v.string("missing"))
        assertNull(v.string("c"))
        assertNull(v.long("b"))
        assertNull(v.nonEmptyString("missing"))
    }

    @Test
    fun keepsLargeIntegersExact() {
        val v = Json.parseObject("""{"n":9007199254740993}""")
        assertEquals(9007199254740993L, v.long("n"))
    }

    @Test
    fun decodesEscapesAndSurrogatePairs() {
        val v = Json.parseObject("""{"s":"你😀\/\t"}""")
        assertEquals("你😀/\t", v.string("s"))
        assertEquals("""{"s":"你😀/\t"}""", Json.stringify(v))
    }

    @Test
    fun escapesControlCharacters() {
        val o = jsonObject { put("s", "a\u0001b") }
        assertEquals("""{"s":"a\u0001b"}""", Json.stringify(o))
    }

    @Test
    fun rejectsMalformedInput() {
        for (bad in listOf("", "{", "{\"a\":}", "[1,]", "{\"a\":1}x", "\"\u0001\"", "01", "-", "1.", "tru", "{'a':1}")) {
            try {
                Json.parse(bad)
                fail("parsed: $bad")
            } catch (e: JsonException) {
                assertTrue(e.message!!.isNotEmpty())
            }
        }
    }

    @Test
    fun rejectsExcessiveNesting() {
        val deep = "[".repeat(300) + "]".repeat(300)
        try {
            Json.parse(deep)
            fail("parsed deep nesting")
        } catch (e: JsonException) {
            assertTrue(e.message!!.contains("deep"))
        }
    }

    @Test
    fun builderSkipsNulls() {
        val o = jsonObject {
            put("action", "pub")
            put("topic", null as String?)
            put("seq", 2)
        }
        assertEquals("""{"action":"pub","seq":2}""", Json.stringify(o))
    }
}
