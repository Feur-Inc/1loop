// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.app.ActivityManager
import android.app.ApplicationExitInfo
import android.content.Context
import android.os.Build
import android.os.Process
import android.os.SystemClock
import android.util.Log
import androidx.annotation.RequiresApi
import java.io.File
import java.net.URLEncoder
import org.json.JSONObject

/**
 * After a crash, the next start offers to report it, like the desktop
 * (internal/crash): the whole anonymized report on screen, then a GitHub
 * link the user opens and submits from their own account. 1loop sends
 * nothing itself.
 *
 * An uncaught exception is saved by [install] before the process dies;
 * on Android 11+ the system's exit reasons add native crashes and ANRs.
 */
object CrashReports {
    const val NEW_ISSUE = "https://github.com/Feur-Inc/1loop/issues/new"
    const val MAX_URL = 8000
    private const val MAX_AGE = 5L * 24 * 3600 * 1000
    private const val PREFS = "1loop"
    private const val OFF = "crash-reports-off"
    private const val EXIT_SEEN = "crash-exit-seen"

    private fun file(context: Context) = File(context.filesDir, "crash.json")

    /** Saves an uncaught exception before the process ends. */
    fun install(context: Context) {
        val app = context.applicationContext
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        if (previous is Handler) return
        Thread.setDefaultUncaughtExceptionHandler(Handler(app, previous))
    }

    private class Handler(val context: Context, val previous: Thread.UncaughtExceptionHandler?) : Thread.UncaughtExceptionHandler {
        override fun uncaughtException(thread: Thread, error: Throwable) {
            runCatching {
                val ranFor = if (Build.VERSION.SDK_INT >= 24) SystemClock.elapsedRealtime() - Process.getStartElapsedRealtime() else 0L
                file(context).writeText(JSONObject()
                    .put("time", System.currentTimeMillis())
                    .put("ranFor", ranFor)
                    .put("kind", "exception")
                    .put("trace", Log.getStackTraceString(error))
                    .toString())
            }
            previous?.uncaughtException(thread, error)
        }
    }

    fun enabled(context: Context) = !context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getBoolean(OFF, false)

    fun setEnabled(context: Context, on: Boolean) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putBoolean(OFF, !on).apply()
    }

    /** The last run's crash, taken: each one is offered once. */
    fun take(context: Context): CrashReport? {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        var crash: CrashReport? = null
        val saved = file(context)
        if (saved.exists()) {
            crash = runCatching {
                val json = JSONObject(saved.readText())
                CrashReport(json.getLong("time"), json.optLong("ranFor"), json.optString("kind"), json.optString("trace"))
            }.getOrNull()
            saved.delete()
        }
        if (Build.VERSION.SDK_INT >= 30) {
            val seen = prefs.getLong(EXIT_SEEN, -1)
            val exits = runCatching {
                context.getSystemService(ActivityManager::class.java).getHistoricalProcessExitReasons(context.packageName, 0, 10)
            }.getOrDefault(emptyList())
            exits.maxOfOrNull { it.timestamp }?.let { newest -> prefs.edit().putLong(EXIT_SEEN, maxOf(newest, seen)).apply() }
            // The first start of this version only learns where the list is.
            if (crash == null && seen >= 0) {
                exits.filter { it.timestamp > seen }.firstOrNull {
                    it.reason == ApplicationExitInfo.REASON_CRASH_NATIVE || it.reason == ApplicationExitInfo.REASON_ANR
                }?.let { exit -> crash = fromExit(exit) }
            }
        }
        val found = crash ?: return null
        if (System.currentTimeMillis() - found.time > MAX_AGE || !enabled(context)) return null
        return found
    }

    @RequiresApi(30)
    private fun fromExit(exit: ApplicationExitInfo): CrashReport {
        val anr = exit.reason == ApplicationExitInfo.REASON_ANR
        val trace = StringBuilder(if (anr) "ANR: the app stopped responding" else "Native crash")
        exit.description?.let { trace.append(": ").append(it) }
        if (anr) {
            // The main thread's stack, from the system's dump.
            runCatching {
                exit.traceInputStream?.bufferedReader()?.useLines { lines ->
                    val main = lines.dropWhile { !it.startsWith("\"main\"") }.takeWhile { it.isNotBlank() }.take(60).toList()
                    if (main.isNotEmpty()) trace.append("\n\n").append(main.joinToString("\n"))
                }
            }
        }
        return CrashReport(exit.timestamp, 0, if (anr) "anr" else "native", trace.toString())
    }
}

/** One crash, as it was saved. */
data class CrashReport(val time: Long, val ranFor: Long, val kind: String, val trace: String) {
    /** The report, scrubbed: what the dialog shows and the link carries. */
    fun report(context: Context): Report {
        val version = runCatching { context.packageManager.getPackageInfo(context.packageName, 0).versionName }.getOrNull().orEmpty()
        return Report(
            version = version,
            system = "Android ${Build.VERSION.RELEASE} (API ${Build.VERSION.SDK_INT})",
            arch = Build.SUPPORTED_ABIS.firstOrNull().orEmpty(),
            ranFor = ranFor,
            kind = kind,
            trace = Scrubber().trace(trace),
        )
    }
}

class Report(val version: String, val system: String, val arch: String, val ranFor: Long, val kind: String, trace: String) {
    private val lines = trace.lines()

    val title: String = "Crash: " + when (kind) {
        "anr" -> "the phone app stopped responding"
        "native" -> "the phone app crashed in native code"
        else -> lines.firstOrNull { it.isNotBlank() }?.trim()?.let { if (it.length > 90) it.take(90) + "…" else it } ?: "the phone app closed unexpectedly"
    }

    fun body(traceLines: Int = -1): String = buildString {
        append("1loop closed unexpectedly. This report was made by 1loop after the crash and anonymized: paths, names, addresses, secrets and everything written in the chats were taken out.\n\n")
        append("- **1loop:** phone $version, APK\n")
        append("- **System:** $system, $arch\n")
        if (ranFor > 0) append("- **Ran for:** ${duration(ranFor)}\n")
        if (lines.any { it.isNotBlank() }) {
            val shown = if (traceLines in 0 until lines.size) lines.take(traceLines) else lines
            append("\n### Trace\n\n```text\n").append(shown.joinToString("\n")).append("\n```\n")
            if (shown.size < lines.size) append("\n_${lines.size - shown.size} more lines were cut to fit the link._\n")
        }
    }

    /** The full report, as Copy report copies it. */
    val text get() = "# $title\n\n" + body()

    /** The new issue's link, and whether the trace was cut to fit it. */
    fun url(): Pair<String, Boolean> {
        fun link(lines: Int) = CrashReports.NEW_ISSUE + "?title=" + encode(title) + "&body=" + encode(body(lines)) + "&labels=bug"
        var url = link(-1)
        var lines = this.lines.size - 1
        while (url.length > CrashReports.MAX_URL && lines >= 0) url = link(lines--)
        return url to (lines < this.lines.size - 1)
    }

    private fun encode(text: String) = URLEncoder.encode(text, "UTF-8")

    private fun duration(millis: Long): String {
        val minutes = millis / 60_000
        return when {
            minutes < 1 -> "${millis / 1000} s"
            minutes < 60 -> "$minutes min"
            minutes < 48 * 60 -> "${minutes / 60} h ${minutes % 60} min"
            else -> "${minutes / 1440} days"
        }
    }
}

/**
 * The scrubber, the same as the desktop's (internal/crash/scrub.go) for
 * text; their shared cases are in internal/crash/testdata. A Java stack's
 * frames are code locations and stay; the messages are scrubbed.
 */
class Scrubber(home: String = "", private val names: List<String> = emptyList()) {
    private val home = home.trimEnd('/', '\\').replace('\\', '/')

    fun trace(trace: String): String = trace.replace("\r", "").lines().joinToString("\n") { line ->
        val exception = Regex("""^(\s*(?:Caused by|Suppressed): |)([\w$]+(?:\.[\w$]+)+)(?:: (.*))?$""").find(line)
        when {
            Regex("""^\s*at [\w$.<>-]+\(.*\)$""").matches(line) -> line // a code location
            Regex("""^\s*\.\.\. \d+ more$""").matches(line) -> line
            // The exception's class stays, its message is scrubbed.
            exception != null -> exception.groupValues[1] + exception.groupValues[2] + (exception.groups[3]?.let { ": " + text(it.value) } ?: "")
            else -> text(line)
        }
    }.trim()

    fun text(input: String): String {
        var text = input.replace("\r", "")
        text = url.replace(text, "<url>")
        text = email.replace(text, "<email>")
        for (pattern in secrets) text = pattern.replace(text, "<secret>")
        text = bearer.replace(text, "<secret>")
        text = namedSecret.replace(text) { match ->
            val (key, separator, value) = match.destructured
            if ((separator.isBlank() && !key.equals("bearer", true)) || value.startsWith("<") || isNumber(value)) match.value
            else key + separator + "<secret>"
        }
        text = quoted.replace(text) { it.value.first() + "…" + it.value.last() }
        text = paths(text)
        text = id.replace(text, "<id>")
        text = longToken.replace(text) { if (it.value.any(Char::isDigit) && it.value.any { c -> c in 'a'..'z' || c in 'A'..'Z' }) "<secret>" else it.value }
        text = ipv4.replace(text) { match ->
            val host = match.value.substringBefore(':')
            if (host.split('.').any { (it.length > 1 && it[0] == '0') || (it.length == 3 && it > "255") }) match.value else "<ip>"
        }
        text = ipv6.replace(text, "<ip>")
        text = ipv6Letters.replace(text) { if (it.value.lowercase().any { c -> c in 'a'..'f' }) "<ip>" else it.value }
        text = host.replace(text, "<host>")
        text = hideNames(text)
        text = namedThing.replace(text) { match ->
            val (key, separator, value) = match.destructured
            when {
                separator == " " && key.lowercase() !in spacedThings -> match.value
                value.first() in "<~\"'`“«" || value.lowercase() in safeWords || isNumber(value) -> match.value
                else -> key + separator + "<name>"
            }
        }
        return text
    }

    private fun paths(input: String): String {
        var text = windowsPath.replace(input) { path(it.value.replace('\\', '/')) }
        text = absolutePath.replace(text) { match ->
            val (before, raw) = match.destructured
            val kept = raw.trimEnd(':', '.', ',')
            before + path(kept) + raw.substring(kept.length)
        }
        text = relativePath.replace(text) { match ->
            val (before, path) = match.destructured
            if (modulePaths.any(path::startsWith) || "@v" in path) match.value else "$before<path>"
        }
        return fileName.replace(text) { match ->
            val (before, name) = match.destructured
            if (name.lowercase() in ownFiles) match.value else "$before<file>"
        }
    }

    private fun path(input: String): String {
        var path = input
        if (path == "~" || path == "/") return path
        if (home.isNotEmpty() && (path == home || path.startsWith("$home/"))) path = "~" + path.removePrefix(home)
        if (path.startsWith("~")) return if (ownFolders.any { path == it || path.startsWith("$it/") }) path else "~/<path>"
        if (systemFolders.any(path::startsWith) && !path.startsWith("/home/")) return path
        return "<path>"
    }

    private fun hideNames(input: String): String {
        var text = input
        for (name in names.map(String::trim).filter { it.length >= 3 }.distinctBy(String::lowercase).sortedByDescending(String::length)) {
            val pattern = Regex("(^|[^A-Za-z0-9])" + Regex.escape(name) + "($|[^A-Za-z0-9])", RegexOption.IGNORE_CASE)
            while (pattern.containsMatchIn(text)) text = pattern.replace(text, "$1<name>$2")
        }
        return text
    }

    private fun isNumber(text: String): Boolean {
        if (text.isEmpty()) return false
        if (text.startsWith("0x") && text.length > 2 && text.drop(2).all { it in "0123456789abcdefABCDEF" }) return true
        return text.all { it.isDigit() || it == '.' }
    }

    private companion object {
        val url = Regex("""\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"'<>()\[\]{}]+""")
        val email = Regex("""\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+\b""")
        val secrets = listOf(
            Regex("""\b(sk-ant-|sk-proj-|sk-|ghp_|gho_|ghs_|ghu_|github_pat_|glpat-|xox[abprs]-|AIza|hf_|npm_)[A-Za-z0-9_\-]{8,}"""),
            Regex("""\bAKIA[0-9A-Z]{16}\b"""),
            Regex("""\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}(\.[A-Za-z0-9_-]+)?"""),
            Regex("""-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(-----END [A-Z ]*PRIVATE KEY-----|$)"""),
        )
        val bearer = Regex("""(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+""")
        val namedSecret = Regex("""(?i)\b((?:api[_-]?)?key|token|secret|password|passwd|pwd|authorization|cookie|session|bearer|code)(\s*[=:]\s*|\s+)("[^"]*"|'[^']*'|[^\s,;&)]+)""")
        val longToken = Regex("""\b[A-Za-z0-9_+/=-]{24,}\b""")
        val id = Regex("""\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b|\b[0-9a-f]{16,}\b""")
        val quoted = Regex("\"(?:[^\"\\\\\n]|\\\\.)*\"|`[^`\n]*`|“[^”\n]*”|«[^»\n]*»")
        val windowsPath = Regex("""\b[A-Za-z]:\\[^\s"'<>|]*""")
        val absolutePath = Regex("""(^|[\s(\[=,:'])(~?/[^\s"'<>()\[\],;]*|~)""")
        val relativePath = Regex("""(^|[\s(\[=,:'])((?:[\w.@-]+/)+[\w@-]+\.[A-Za-z0-9]{1,5})\b""")
        val fileName = Regex("""(?i)(^|[\s(\[=,:'])([\w-]+\.(?:go|py|js|mjs|ts|tsx|jsx|kt|kts|java|rs|c|h|cc|cpp|hpp|cs|rb|php|swift|lua|sh|zsh|bash|ps1|md|txt|rst|json|jsonl|yaml|yml|toml|ini|cfg|conf|env|lock|xml|html|htm|css|scss|svg|png|jpe?g|gif|webp|pdf|docx?|xlsx?|pptx?|csv|log|sql|db|sqlite|zip|tar|gz|tgz|mp3|mp4|mov|wav|apk|deb|exe|dmg|key|pem|crt))\b""")
        val ipv4 = Regex("""\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b""")
        val ipv6 = Regex("""(?i)\[?\b(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}\b\]?|\[?(?:[0-9a-f]{1,4}:)*[0-9a-f]{0,4}::(?:[0-9a-f]{1,4}:)*[0-9a-f]{1,4}\b\]?""")
        val ipv6Letters = Regex("""(?i)\b(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}\b""")
        val host = Regex("""\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:com|net|org|io|dev|app|ai|co|me|fr|de|uk|eu|us|ca|nl|be|ch|es|it|jp|cn|ru|br|in|xyz|cloud|sh|local|lan|home|internal|arpa|onion)(?::\d{1,5})?\b""")
        val namedThing = Regex("""(?i)\b(chat|title|prompt|project|workspace|machine|host|user|folder|repository|repo|branch|model output|tool args|args|arguments)(=|: | )([^\s<>,;)]+)""")
        val spacedThings = setOf("project", "workspace", "machine", "host", "folder", "repository", "repo", "branch")
        val safeWords = setOf("is", "was", "has", "in", "of", "to", "and", "or", "not", "the", "a", "an", "for", "on", "with", "from", "ready", "list", "screen", "opened", "closed", "saved")
        val ownFolders = listOf("~/.config/1loop", "~/.cache/1loop", "~/.local/share/1loop", "~/.config/1loop-server")
        val systemFolders = listOf("/usr/", "/lib/", "/lib64/", "/lib32/", "/bin/", "/sbin/", "/etc/", "/proc/", "/sys/", "/dev/", "/nix/store/", "/app/", "/snap/", "/system/", "/apex/", "/vendor/", "/data/app/")
        val modulePaths = listOf("internal/", "cmd/", "third_party/", "android/", "1loop/", "io/oneloop/")
        val ownFiles = setOf("state.json", "dev.log", "crash.log", "hooks.json", "settings.json", "main.go")
    }
}
