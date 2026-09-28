package com.cesarpetrescu.ledger

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.job.JobInfo
import android.app.job.JobParameters
import android.app.job.JobScheduler
import android.app.job.JobService
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import org.json.JSONObject
import java.time.Instant
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneId

/** One thing worth telling the owner about, keyed so it is told once; at is when it became news. */
data class Nudge(val key: String, val entryId: String, val title: String, val text: String, val at: Instant? = null)

/**
 * Tells the owner when an agent asks them something or a todo becomes
 * overdue. The phone asks the owner's own server about every 15 minutes;
 * nothing goes through a push service.
 */
object Notifier {
    const val JOB_ID = 4217
    private const val CHANNEL = "needs-you"
    private const val MAX_SHOWN = 5

    private fun prefs(context: Context) = context.getSharedPreferences("notify", Context.MODE_PRIVATE)
    fun enabled(context: Context) = prefs(context).getBoolean("enabled", false)
    fun promptDismissed(context: Context) = prefs(context).getBoolean("prompt-dismissed", false)
    fun dismissPrompt(context: Context) = prefs(context).edit().putBoolean("prompt-dismissed", true).apply()

    /** schedule is false only in tests, so the system job cannot race them. */
    fun setEnabled(context: Context, on: Boolean, schedule: Boolean = true) {
        // Starting over means only what arrives from now on is announced.
        prefs(context).edit().putBoolean("enabled", on).putBoolean("seeded", false).remove("seen")
            .putLong("enabled-at", System.currentTimeMillis()).apply()
        val jobs = context.getSystemService(JobScheduler::class.java)
        if (!on || !schedule) return jobs.cancel(JOB_ID)
        jobs.schedule(JobInfo.Builder(JOB_ID, ComponentName(context, InboxCheckJob::class.java))
            .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
            .setPeriodic(15 * 60 * 1000L)
            .setPersisted(true)
            .build())
        // Learn what is already there now, so anything arriving from here on is announced.
        val app = context.applicationContext
        Thread { check(app) }.start()
    }

    /**
     * What deserves a notification: the inbox's open asks, and every overdue
     * todo (the inbox lists only the most urgent few todos).
     */
    fun nudges(inbox: JSONObject, todos: List<JSONObject>, today: LocalDate = LocalDate.now()): List<Nudge> = buildList {
        val asks = inbox.rows("needs_you")
        asks.forEach { entry ->
            val ask = entry.optJSONObject("meta")?.text("ask").orEmpty().ifBlank { entryTitle(entry) }
            add(Nudge("a:${entry.text("id")}", entry.text("id"), "${entry.text("source")} asks you", "$ask · ${entry.text("project_name")}",
                runCatching { OffsetDateTime.parse(entry.text("created_at")).toInstant() }.getOrNull()))
        }
        val asked = asks.map { it.text("id") }.toSet()
        todos.filter { it.text("id") !in asked }.forEach { entry ->
            val due = runCatching { LocalDate.parse(entry.optJSONObject("meta")?.text("due")) }.getOrNull()
            // A new due date is news again.
            if (due != null && due < today) add(Nudge("t:${entry.text("id")}:$due", entry.text("id"), "Overdue: ${entryTitle(entry)}", entry.text("project_name"),
                due.plusDays(1).atStartOfDay(ZoneId.systemDefault()).toInstant()))
        }
    }

    /** The nudges not announced before, and what to remember as announced. */
    fun fresh(current: List<Nudge>, seen: Set<String>): Pair<List<Nudge>, Set<String>> =
        current.filter { it.key !in seen } to current.map { it.key }.toSet()

    /**
     * What to announce. Once a baseline exists, everything new; before that
     * (the first check after turning notifications on, even one that ran late
     * because the first attempt failed), only what became news after then.
     */
    fun toAnnounce(news: List<Nudge>, seeded: Boolean, since: Instant): List<Nudge> =
        if (seeded) news else news.filter { it.at != null && it.at.isAfter(since) }

    /**
     * Asks the server once and posts notifications for anything new. Blocks on
     * the network, so never call it on the main thread. Returns how many it
     * posted. One check at a time, so overlapping runs cannot race the history.
     */
    @Synchronized
    fun check(context: Context): Int {
        if (!enabled(context)) return 0
        // Without permission to show them, keep what was seen so items announce once it returns.
        if (!allowed(context)) return 0
        val api = SessionStore(context).read() ?: return 0
        val (inbox, todos) = try { api.request("GET", "/inbox") to overdueTodos(api) } catch (_: Exception) { return 0 }
        // Turned off or blocked while this was asking the server: record and announce nothing.
        if (!enabled(context) || !allowed(context)) return 0
        val prefs = prefs(context)
        // Entry IDs belong to one server: signing in to another starts over. The
        // first check after opting in has no server yet, and that is not a switch.
        val stored = prefs.getString("origin", null)
        val sameServer = stored == null || stored == api.origin
        val seen = if (sameServer) prefs.getStringSet("seen", emptySet()).orEmpty() else emptySet()
        val (news, remember) = fresh(nudges(inbox, todos), seen)
        val seeded = sameServer && prefs.getBoolean("seeded", false)
        // On another server nothing predates the switch; otherwise the opt-in moment is the boundary.
        val since = if (sameServer) Instant.ofEpochMilli(prefs.getLong("enabled-at", 0)) else Instant.now()
        prefs.edit().putStringSet("seen", remember).putBoolean("seeded", true).putString("origin", api.origin).apply()
        val announce = toAnnounce(news, seeded, since)
        if (announce.isEmpty()) return 0
        val manager = context.getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel(CHANNEL, "Needs you", NotificationManager.IMPORTANCE_DEFAULT).apply {
            description = "When an agent asks you something or a todo becomes overdue"
        })
        announce.take(MAX_SHOWN).forEach { nudge ->
            manager.notify(nudge.key.hashCode(), android.app.Notification.Builder(context, CHANNEL)
                .setSmallIcon(R.mipmap.ic_launcher)
                .setContentTitle(nudge.title)
                .setContentText(nudge.text)
                .setStyle(android.app.Notification.BigTextStyle().bigText(nudge.text))
                .setAutoCancel(true)
                .setContentIntent(open(context, "entry-view/${segment(nudge.entryId)}", nudge.key.hashCode(), api.origin))
                .build())
        }
        if (announce.size > MAX_SHOWN) manager.notify(CHANNEL.hashCode(), android.app.Notification.Builder(context, CHANNEL)
            .setSmallIcon(R.mipmap.ic_launcher)
            .setContentTitle("${announce.size - MAX_SHOWN} more need you")
            .setContentText("Open the Inbox to see them all.")
            .setAutoCancel(true)
            .setContentIntent(open(context, "inbox", CHANNEL.hashCode(), api.origin))
            .build())
        return announce.size
    }

    /** Every open, awake todo due before today, a page at a time. */
    private fun overdueTodos(api: Api): List<JSONObject> {
        val out = mutableListOf<JSONObject>()
        var before = ""
        while (true) {
            val page = api.request("GET", "/entries?kind=todo&status=open&awake=1&due_before=${LocalDate.now()}&limit=200" + if (before.isBlank()) "" else "&before=${segment(before)}")
            out += page.rows("entries")
            val next = page.text("next_before")
            // A cursor that does not move would page forever.
            if (next.isBlank() || next == before) return out
            before = next
        }
    }

    /** Android 13+ asks for this permission; older versions grant it. */
    fun permissionMissing(context: Context) = Build.VERSION.SDK_INT >= 33 &&
        context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED

    /** Whether a notification would actually show: permission, app switch, and channel. */
    fun allowed(context: Context): Boolean {
        val manager = context.getSystemService(NotificationManager::class.java)
        val permitted = Build.VERSION.SDK_INT < 33 || context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
        val channel = manager.getNotificationChannel(CHANNEL)
        return permitted && manager.areNotificationsEnabled() && (channel == null || channel.importance != NotificationManager.IMPORTANCE_NONE)
    }

    // The server rides along: an entry ID means nothing on another server.
    private fun open(context: Context, route: String, requestCode: Int, origin: String): PendingIntent =
        PendingIntent.getActivity(context, requestCode, Intent(context, MainActivity::class.java)
            .putExtra(MainActivity.ROUTE, route)
            .putExtra(MainActivity.ORIGIN, origin)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
}

/** Runs [Notifier.check] off the main thread when the system schedules it. */
class InboxCheckJob : JobService() {
    override fun onStartJob(params: JobParameters): Boolean {
        Thread {
            try { Notifier.check(applicationContext) } finally { jobFinished(params, false) }
        }.start()
        return true
    }

    // Periodic: the next run comes anyway, so no retry that could overlap this one.
    override fun onStopJob(params: JobParameters) = false
}
