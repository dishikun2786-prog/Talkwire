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

/** The signed-in user of the sample. A real app has its own sign-in; Talkwire only needs the token. */
data class Account(val userId: String, val name: String, val token: String)

/**
 * Sign-in against the sample host's `/dev/login`, which signs anyone in by
 * name. Stands in for the app's own account system.
 */
class DevAccount(context: Context, private val http: OkHttpClient) {
    private val prefs = context.getSharedPreferences("talkwire_sample_account", Context.MODE_PRIVATE)

    fun current(): Account? {
        val id = prefs.getString("userId", null) ?: return null
        val token = prefs.getString("token", null) ?: return null
        return Account(id, prefs.getString("name", null) ?: id, token)
    }

    suspend fun signIn(name: String): Account = withContext(Dispatchers.IO) {
        val req = Request.Builder()
            .url(SampleConfig.HOST_URL + "/dev/login")
            .post(jsonObject { put("name", name) }.toString().toRequestBody("application/json".toMediaType()))
            .build()
        http.newCall(req).execute().use { resp ->
            val text = resp.body?.string() ?: ""
            val o = runCatching { Json.parseObject(text) }.getOrNull()
            if (!resp.isSuccessful || o == null) throw IllegalStateException(o?.string("error") ?: "HTTP ${resp.code}")
            val account = Account(o.string("userId")!!, o.string("name") ?: name, o.string("accessToken")!!)
            prefs.edit().putString("userId", account.userId).putString("name", account.name).putString("token", account.token).apply()
            account
        }
    }

    fun signOut() {
        prefs.edit().clear().apply()
    }
}

object SampleConfig {
    /** The sample host and the IM server, reached on the phone through `adb reverse`. */
    val HOST_URL: String = BuildConfig.HOST_URL
    val IM_URL: String = BuildConfig.IM_URL
    /** The web sample, for links other people open in a browser. */
    val WEB_URL: String = BuildConfig.WEB_URL
}
