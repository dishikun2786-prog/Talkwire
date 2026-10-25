package io.imkit.core.json

/**
 * A small JSON model, parser and writer.
 *
 * The SDK carries its own instead of using `org.json`: Android ships one
 * implementation of `org.json` and the JVM artifact is another, and they
 * disagree on details that matter here (Android's `optString` turns a JSON
 * `null` into the string `"null"`). Tests on the JVM would then check
 * different behaviour from what runs on a phone. With this codec the bytes
 * are read the same way everywhere.
 *
 * Accessors on [JsonObject] treat a missing field and a field of another type
 * the same way, returning `null`; the protocol says an absent field means the
 * zero value (protocol spec 3.2), so callers decide the default explicitly.
 */
public sealed class JsonValue {
    override fun toString(): String = Json.stringify(this)
}

public object JsonNull : JsonValue()

public data class JsonBool(val value: Boolean) : JsonValue() {
    override fun toString(): String = Json.stringify(this)
}

/** A number kept as written, so 64-bit integers survive without rounding. */
public class JsonNumber(public val raw: String) : JsonValue() {
    public fun toLongOrNull(): Long? = raw.toLongOrNull() ?: raw.toDoubleOrNull()?.let { d ->
        if (d % 1.0 == 0.0 && d >= Long.MIN_VALUE.toDouble() && d <= Long.MAX_VALUE.toDouble()) d.toLong() else null
    }

    public fun toDouble(): Double = raw.toDouble()

    override fun equals(other: Any?): Boolean = other is JsonNumber && (other.raw == raw || other.toDouble() == toDouble())

    override fun hashCode(): Int = toDouble().hashCode()

    override fun toString(): String = raw
}

public data class JsonString(val value: String) : JsonValue() {
    override fun toString(): String = Json.stringify(this)
}

public data class JsonArray(val items: List<JsonValue>) : JsonValue() {
    public val size: Int get() = items.size

    public operator fun get(index: Int): JsonValue = items[index]

    public fun objects(): List<JsonObject> = items.filterIsInstance<JsonObject>()

    override fun toString(): String = Json.stringify(this)
}

public class JsonObject(fields: Map<String, JsonValue>) : JsonValue() {
    public val fields: Map<String, JsonValue> = LinkedHashMap(fields)

    public operator fun get(key: String): JsonValue? = fields[key]

    public fun has(key: String): Boolean = fields.containsKey(key)

    /** The string value, or null when absent, `null`, or not a string. */
    public fun string(key: String): String? = (fields[key] as? JsonString)?.value

    /** Like [string], but an empty string also counts as absent. */
    public fun nonEmptyString(key: String): String? = string(key)?.takeIf { it.isNotEmpty() }

    public fun long(key: String): Long? = (fields[key] as? JsonNumber)?.toLongOrNull()

    public fun int(key: String): Int? = long(key)?.takeIf { it in Int.MIN_VALUE..Int.MAX_VALUE }?.toInt()

    public fun bool(key: String): Boolean? = (fields[key] as? JsonBool)?.value

    public fun obj(key: String): JsonObject? = fields[key] as? JsonObject

    public fun array(key: String): JsonArray? = fields[key] as? JsonArray

    /** A copy with [key] set to [value]; `null` removes it. */
    public fun with(key: String, value: JsonValue?): JsonObject {
        val copy = LinkedHashMap(fields)
        if (value == null) copy.remove(key) else copy[key] = value
        return JsonObject(copy)
    }

    override fun equals(other: Any?): Boolean = other is JsonObject && other.fields == fields

    override fun hashCode(): Int = fields.hashCode()

    override fun toString(): String = Json.stringify(this)
}

public class JsonException(message: String) : IllegalArgumentException(message)

public object Json {
    private const val MAX_DEPTH = 256

    public fun parse(text: String): JsonValue {
        val p = Parser(text)
        p.skipWs()
        val v = p.value(0)
        p.skipWs()
        if (p.pos != text.length) throw JsonException("trailing data at ${p.pos}")
        return v
    }

    public fun parseObject(text: String): JsonObject =
        parse(text) as? JsonObject ?: throw JsonException("not a JSON object")

    public fun stringify(value: JsonValue): String = StringBuilder().also { write(it, value) }.toString()

    private fun write(sb: StringBuilder, value: JsonValue) {
        when (value) {
            JsonNull -> sb.append("null")
            is JsonBool -> sb.append(value.value)
            is JsonNumber -> sb.append(value.raw)
            is JsonString -> quote(sb, value.value)
            is JsonArray -> {
                sb.append('[')
                value.items.forEachIndexed { i, v ->
                    if (i > 0) sb.append(',')
                    write(sb, v)
                }
                sb.append(']')
            }
            is JsonObject -> {
                sb.append('{')
                var first = true
                for ((k, v) in value.fields) {
                    if (!first) sb.append(',')
                    first = false
                    quote(sb, k)
                    sb.append(':')
                    write(sb, v)
                }
                sb.append('}')
            }
        }
    }

    private fun quote(sb: StringBuilder, s: String) {
        sb.append('"')
        for (c in s) {
            when (c) {
                '"' -> sb.append("\\\"")
                '\\' -> sb.append("\\\\")
                '\n' -> sb.append("\\n")
                '\r' -> sb.append("\\r")
                '\t' -> sb.append("\\t")
                '\b' -> sb.append("\\b")
                '\u000C' -> sb.append("\\f")
                else -> if (c < ' ') {
                    sb.append("\\u").append(String.format("%04x", c.code))
                } else {
                    sb.append(c)
                }
            }
        }
        sb.append('"')
    }

    private class Parser(val s: String) {
        var pos = 0

        fun skipWs() {
            while (pos < s.length && (s[pos] == ' ' || s[pos] == '\t' || s[pos] == '\n' || s[pos] == '\r')) pos++
        }

        fun fail(msg: String): Nothing = throw JsonException("$msg at $pos")

        fun value(depth: Int): JsonValue {
            if (depth > MAX_DEPTH) fail("nesting too deep")
            if (pos >= s.length) fail("unexpected end")
            return when (val c = s[pos]) {
                '{' -> obj(depth)
                '[' -> arr(depth)
                '"' -> JsonString(str())
                't' -> literal("true", JsonBool(true))
                'f' -> literal("false", JsonBool(false))
                'n' -> literal("null", JsonNull)
                else -> if (c == '-' || c in '0'..'9') num() else fail("unexpected '$c'")
            }
        }

        fun literal(word: String, v: JsonValue): JsonValue {
            if (!s.startsWith(word, pos)) fail("bad literal")
            pos += word.length
            return v
        }

        fun obj(depth: Int): JsonObject {
            pos++ // {
            val fields = LinkedHashMap<String, JsonValue>()
            skipWs()
            if (pos < s.length && s[pos] == '}') {
                pos++
                return JsonObject(fields)
            }
            while (true) {
                skipWs()
                if (pos >= s.length || s[pos] != '"') fail("expected key")
                val key = str()
                skipWs()
                if (pos >= s.length || s[pos] != ':') fail("expected ':'")
                pos++
                skipWs()
                fields[key] = value(depth + 1)
                skipWs()
                if (pos >= s.length) fail("unexpected end")
                when (s[pos]) {
                    ',' -> pos++
                    '}' -> {
                        pos++
                        return JsonObject(fields)
                    }
                    else -> fail("expected ',' or '}'")
                }
            }
        }

        fun arr(depth: Int): JsonArray {
            pos++ // [
            val items = ArrayList<JsonValue>()
            skipWs()
            if (pos < s.length && s[pos] == ']') {
                pos++
                return JsonArray(items)
            }
            while (true) {
                skipWs()
                items += value(depth + 1)
                skipWs()
                if (pos >= s.length) fail("unexpected end")
                when (s[pos]) {
                    ',' -> pos++
                    ']' -> {
                        pos++
                        return JsonArray(items)
                    }
                    else -> fail("expected ',' or ']'")
                }
            }
        }

        fun str(): String {
            pos++ // opening quote
            val sb = StringBuilder()
            while (true) {
                if (pos >= s.length) fail("unterminated string")
                val c = s[pos++]
                when {
                    c == '"' -> return sb.toString()
                    c == '\\' -> {
                        if (pos >= s.length) fail("bad escape")
                        when (val e = s[pos++]) {
                            '"' -> sb.append('"')
                            '\\' -> sb.append('\\')
                            '/' -> sb.append('/')
                            'b' -> sb.append('\b')
                            'f' -> sb.append('\u000C')
                            'n' -> sb.append('\n')
                            'r' -> sb.append('\r')
                            't' -> sb.append('\t')
                            'u' -> {
                                if (pos + 4 > s.length) fail("bad \\u escape")
                                val code = s.substring(pos, pos + 4).toIntOrNull(16) ?: fail("bad \\u escape")
                                pos += 4
                                sb.append(code.toChar())
                            }
                            else -> fail("bad escape '\\$e'")
                        }
                    }
                    c < ' ' -> fail("control character in string")
                    else -> sb.append(c)
                }
            }
        }

        fun num(): JsonNumber {
            val start = pos
            if (s[pos] == '-') pos++
            if (pos >= s.length) fail("bad number")
            if (s[pos] == '0') {
                pos++
            } else if (s[pos] in '1'..'9') {
                while (pos < s.length && s[pos] in '0'..'9') pos++
            } else {
                fail("bad number")
            }
            if (pos < s.length && s[pos] == '.') {
                pos++
                if (pos >= s.length || s[pos] !in '0'..'9') fail("bad number")
                while (pos < s.length && s[pos] in '0'..'9') pos++
            }
            if (pos < s.length && (s[pos] == 'e' || s[pos] == 'E')) {
                pos++
                if (pos < s.length && (s[pos] == '+' || s[pos] == '-')) pos++
                if (pos >= s.length || s[pos] !in '0'..'9') fail("bad number")
                while (pos < s.length && s[pos] in '0'..'9') pos++
            }
            return JsonNumber(s.substring(start, pos))
        }
    }
}

/** Builds a [JsonObject] in insertion order; `null` values are left out. */
public class JsonObjectBuilder {
    private val fields = LinkedHashMap<String, JsonValue>()

    public fun put(key: String, value: String?) {
        if (value != null) fields[key] = JsonString(value)
    }

    public fun put(key: String, value: Number?) {
        if (value != null) fields[key] = JsonNumber(value.toString())
    }

    public fun put(key: String, value: Boolean?) {
        if (value != null) fields[key] = JsonBool(value)
    }

    public fun put(key: String, value: JsonValue?) {
        if (value != null) fields[key] = value
    }

    public fun putStrings(key: String, values: List<String>?) {
        if (values != null) fields[key] = JsonArray(values.map { JsonString(it) })
    }

    public fun putNumbers(key: String, values: List<Number>?) {
        if (values != null) fields[key] = JsonArray(values.map { JsonNumber(it.toString()) })
    }

    public fun build(): JsonObject = JsonObject(fields)
}

public inline fun jsonObject(block: JsonObjectBuilder.() -> Unit): JsonObject =
    JsonObjectBuilder().apply(block).build()
