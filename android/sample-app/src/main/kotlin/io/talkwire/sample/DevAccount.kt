package io.talkwire.sample

import android.content.Context
import io.talkwire.core.json.Json
import io.talkwire.core.json.jsonObject
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

/**
 * The signed-in user of the sample. A real app has its own sign-in; Talkwire only needs the token.
 * [space] is the sandbox space signed in to; null on the development host.
 */
data class Account(val userId: String, val name: String, val token: String, val space: String? = null)

/**
 * Sign-in against the sample host's `/dev/login`, which signs anyone in by
 * name. Stands in for the app's own account system.
 */
class DevAccount(context: Context, private val http: OkHttpClient) {
    private val prefs = context.getSharedPreferences("talkwire_sample_account", Context.MODE_PRIVATE)

    fun current(): Account? {
        // Signed in to another server (the build switched between the development host and the sandbox).
        if (prefs.getString("host", SampleConfig.HOST_URL) != SampleConfig.HOST_URL) return null
        val id = prefs.getString("userId", null) ?: return null
        val token = prefs.getString("token", null) ?: return null
        return Account(id, prefs.getString("name", null) ?: id, token, prefs.getString("space", null))
    }

    /** The sandbox space last signed in to or made here, to fill in the sign-in page. */
    fun lastSpace(): String? = prefs.getString("lastSpace", null)

    suspend fun signIn(name: String, space: String? = null): Account = withContext(Dispatchers.IO) {
        val body = jsonObject {
            put("name", name)
            if (space != null) put("space", space)
        }
        val o = post("/dev/login", body.toString())
        val account = Account(o.string("userId")!!, o.string("name") ?: name, o.string("accessToken")!!, o.string("space"))
        prefs.edit()
            .putString("host", SampleConfig.HOST_URL)
            .putString("userId", account.userId)
            .putString("name", account.name)
            .putString("token", account.token)
            .putString("space", account.space)
            .apply()
        account.space?.let { prefs.edit().putString("lastSpace", it).apply() }
        account
    }

    /** A new space on the sandbox host; others sign in to it with the code returned. */
    suspend fun createSpace(): String = withContext(Dispatchers.IO) {
        val code = post("/sandbox/spaces", "{}").string("space") ?: throw IllegalStateException("no space in the reply")
        prefs.edit().putString("lastSpace", code).apply()
        code
    }

    private fun post(path: String, json: String): io.talkwire.core.json.JsonObject {
        val req = Request.Builder()
            .url(SampleConfig.HOST_URL + path)
            .post(json.toRequestBody("application/json".toMediaType()))
            .build()
        http.newCall(req).execute().use { resp ->
            val text = resp.body?.string() ?: ""
            val o = runCatching { Json.parseObject(text) }.getOrNull()
            if (!resp.isSuccessful || o == null) throw IllegalStateException(o?.string("error") ?: "HTTP ${resp.code}")
            return o
        }
    }

    fun signOut() {
        val space = lastSpace()
        prefs.edit().clear().apply()
        space?.let { prefs.edit().putString("lastSpace", it).apply() }
    }
}

object SampleConfig {
    /** The sample host and the IM server, reached on the phone through `adb reverse`. */
    val HOST_URL: String = BuildConfig.HOST_URL
    val IM_URL: String = BuildConfig.IM_URL
    /** The web sample, for links other people open in a browser. */
    val WEB_URL: String = BuildConfig.WEB_URL
    /** Built against the public sandbox (`-PtalkwireSandbox=`): sign-in takes a space code. */
    val SANDBOX: Boolean = BuildConfig.SANDBOX
}
