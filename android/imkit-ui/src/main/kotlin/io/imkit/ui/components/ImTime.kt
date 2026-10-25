package io.imkit.ui.components

import android.content.Context
import io.imkit.ui.R
import java.text.SimpleDateFormat
import java.util.Calendar
import java.util.Date
import java.util.Locale
import java.util.TimeZone

/**
 * Times in the chat UI, in the user's language and 12/24-hour setting.
 * [now] and [zone] are parameters so the rules can be tested.
 */
public object ImTime {
    /** Under a bubble: the time today, else the date and time. */
    public fun messageTime(context: Context, ts: Long, now: Long = System.currentTimeMillis(), zone: TimeZone = TimeZone.getDefault()): String {
        val locale = locale(context)
        val time = format(timeSkeleton(context), locale, zone, ts)
        return if (dayDiff(ts, now, zone) == 0) time else format(if (sameYear(ts, now, zone)) "MMMd" else "yMMMd", locale, zone, ts) + " " + time
    }

    /** In the conversation list: "now", minutes, the time today, "yesterday", then a date. Empty for 0. */
    public fun conversationTime(context: Context, ts: Long, now: Long = System.currentTimeMillis(), zone: TimeZone = TimeZone.getDefault()): String {
        if (ts <= 0L) return ""
        val diff = now - ts
        return when {
            diff in 0 until 60_000 -> context.getString(R.string.imkit_just_now)
            diff in 0 until 3_600_000 -> context.getString(R.string.imkit_minutes_ago, (diff / 60_000).toInt())
            dayDiff(ts, now, zone) == 0 -> format(timeSkeleton(context), locale(context), zone, ts)
            dayDiff(ts, now, zone) == 1 -> context.getString(R.string.imkit_yesterday)
            sameYear(ts, now, zone) -> format("MMMd", locale(context), zone, ts)
            else -> format("yMd", locale(context), zone, ts)
        }
    }

    /** Between days in the message list. */
    public fun chatDate(context: Context, ts: Long, now: Long = System.currentTimeMillis(), zone: TimeZone = TimeZone.getDefault()): String =
        when (dayDiff(ts, now, zone)) {
            0 -> context.getString(R.string.imkit_today)
            1 -> context.getString(R.string.imkit_yesterday)
            else -> format(if (sameYear(ts, now, zone)) "MMMdEEE" else "yMMMd", locale(context), zone, ts)
        }

    /** "Last seen …"; empty when unknown, in the future or over a month ago. */
    public fun lastSeen(context: Context, lastSeenAt: Long, now: Long = System.currentTimeMillis()): String {
        if (lastSeenAt <= 0L) return ""
        val d = now - lastSeenAt
        return when {
            d < 0 -> ""
            d < 60_000 -> context.getString(R.string.imkit_online)
            d < 3_600_000 -> context.getString(R.string.imkit_last_seen_minutes, (d / 60_000).toInt())
            d < 86_400_000 -> context.getString(R.string.imkit_last_seen_hours, (d / 3_600_000).toInt())
            d < 30 * 86_400_000L -> context.getString(R.string.imkit_last_seen_days, (d / 86_400_000).toInt())
            else -> ""
        }
    }

    /** Whole calendar days from [a] to [b] in [zone] (0 = same day). */
    internal fun dayDiff(a: Long, b: Long, zone: TimeZone): Int {
        val ca = startOfDay(a, zone)
        val cb = startOfDay(b, zone)
        // Rounding absorbs the hour a daylight-saving change adds or removes.
        return Math.round((cb - ca) / 86_400_000.0).toInt()
    }

    internal fun sameDay(a: Long, b: Long, zone: TimeZone): Boolean = dayDiff(a, b, zone) == 0

    private fun sameYear(a: Long, b: Long, zone: TimeZone): Boolean {
        val ca = Calendar.getInstance(zone).apply { timeInMillis = a }
        val cb = Calendar.getInstance(zone).apply { timeInMillis = b }
        return ca.get(Calendar.YEAR) == cb.get(Calendar.YEAR)
    }

    private fun startOfDay(ts: Long, zone: TimeZone): Long = Calendar.getInstance(zone).apply {
        timeInMillis = ts
        set(Calendar.HOUR_OF_DAY, 0)
        set(Calendar.MINUTE, 0)
        set(Calendar.SECOND, 0)
        set(Calendar.MILLISECOND, 0)
    }.timeInMillis

    private fun timeSkeleton(context: Context): String =
        if (android.text.format.DateFormat.is24HourFormat(context)) "Hm" else "hm"

    private fun format(skeleton: String, locale: Locale, zone: TimeZone, ts: Long): String {
        val pattern = android.text.format.DateFormat.getBestDateTimePattern(locale, skeleton)
        return SimpleDateFormat(pattern, locale).apply { timeZone = zone }.format(Date(ts))
    }

    private fun locale(context: Context): Locale = context.resources.configuration.locales[0]
}
