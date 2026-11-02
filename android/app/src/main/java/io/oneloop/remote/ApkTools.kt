package io.oneloop.remote

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.ContextCompat
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import org.json.JSONObject
import java.io.File

/**
 * The agent's APK tools (install_apk, extract_apk). They go through Android's
 * package manager and installer, not through ADB: the user confirms an install
 * in Android's own dialog, and the app reads the APKs of installed apps itself.
 * Paths are those the agent sees in Debian (/projects/...), checked to stay
 * inside the projects folder of the Debian root.
 */
class ApkTools(private val context: Context, private val root: () -> File?) {
    private fun projectFile(path: String): File {
        val base = File(root() ?: error("Install Debian first."), "projects").canonicalFile
        require(path.startsWith("/projects/")) { "Choose a path under /projects." }
        val file = File(base.parentFile, path.removePrefix("/")).canonicalFile
        require(file.path.startsWith(base.path + File.separator)) { "Choose a path under /projects." }
        return file
    }

    suspend fun execute(name: String, arguments: JSONObject): JSONObject = withContext(Dispatchers.IO) {
        JSONObject().put("text", when (name) {
            "install_apk" -> install(projectFile(arguments.getString("path")))
            "extract_apk" -> extract(arguments.getString("package").trim(), arguments.optString("destination").trim())
            else -> error("Unknown APK tool.")
        })
    }

    private suspend fun install(source: File): String {
        require(source.exists()) { "No such file: ${source.name}." }
        val apks = if (source.isDirectory) source.listFiles { file -> file.isFile && file.name.endsWith(".apk") }.orEmpty().sortedBy { it.name }
        else listOf(source)
        require(apks.isNotEmpty() && apks.all { it.name.endsWith(".apk") }) { "Give an .apk file, or a folder of .apk files." }
        if (!context.packageManager.canRequestPackageInstalls()) {
            runCatching {
                context.startActivity(
                    Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${context.packageName}")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                )
            }
            return "Blocked: Android does not let 1loop install apps yet. Ask the user to turn on \"Install unknown apps\" for 1loop (its settings page was opened), then call install_apk again."
        }
        val label = context.packageManager.getPackageArchiveInfo(apks.first().path, 0)?.packageName ?: apks.first().name
        val installer = context.packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL)
        if (Build.VERSION.SDK_INT >= 31) params.setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_REQUIRED)
        val id = installer.createSession(params)
        val result = CompletableDeferred<Pair<Int, String>>()
        val action = "${context.packageName}.APK_INSTALL.$id"
        val receiver = object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) {
                val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)
                if (status == PackageInstaller.STATUS_PENDING_USER_ACTION) {
                    // Android's own dialog, where the user confirms (and Play Protect speaks).
                    val confirm = if (Build.VERSION.SDK_INT >= 33) intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
                    else @Suppress("DEPRECATION") intent.getParcelableExtra(Intent.EXTRA_INTENT)
                    runCatching { context.startActivity(confirm?.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
                        .onFailure { result.complete(PackageInstaller.STATUS_FAILURE to "Android's install dialog could not be opened from the background: ${it.message}") }
                } else result.complete(status to intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE).orEmpty())
            }
        }
        ContextCompat.registerReceiver(context, receiver, IntentFilter(action), ContextCompat.RECEIVER_NOT_EXPORTED)
        try {
            installer.openSession(id).use { session ->
                for (apk in apks) {
                    apk.inputStream().use { input -> session.openWrite(apk.name, 0, apk.length()).use { output -> input.copyTo(output); session.fsync(output) } }
                }
                val intent = Intent(action).setPackage(context.packageName)
                val pending = android.app.PendingIntent.getBroadcast(
                    context, id, intent, android.app.PendingIntent.FLAG_UPDATE_CURRENT or android.app.PendingIntent.FLAG_MUTABLE,
                )
                session.commit(pending.intentSender)
            }
            val (status, message) = try {
                withTimeout(4 * 60_000L) { result.await() }
            } catch (_: TimeoutCancellationException) {
                runCatching { installer.abandonSession(id) }
                return "Not finished: the user did not answer Android's install dialog within 4 minutes, so the install was cancelled. Ask them, then call install_apk again."
            }
            return describe(status, message, label)
        } catch (failure: Exception) {
            runCatching { installer.abandonSession(id) }
            throw failure
        } finally {
            runCatching { context.unregisterReceiver(receiver) }
        }
    }

    private fun describe(status: Int, message: String, label: String): String = when (status) {
        PackageInstaller.STATUS_SUCCESS -> "Installed: $label."
        PackageInstaller.STATUS_FAILURE_ABORTED -> "Cancelled: the user declined the install of $label in Android's dialog."
        PackageInstaller.STATUS_FAILURE_BLOCKED ->
            "Blocked: Android refused to install $label ($message). Play Protect or a device policy may block it: ask the user to allow it in Play Protect (\"Install anyway\" / turn scanning off for this install), or check that \"Install unknown apps\" is on for 1loop."
        PackageInstaller.STATUS_FAILURE_CONFLICT ->
            "Conflict: $label clashes with an installed app ($message), usually another signature or an older version code. The installed app must be uninstalled first (this deletes its data): ask the user."
        PackageInstaller.STATUS_FAILURE_INCOMPATIBLE -> "Incompatible: $label does not fit this phone ($message): wrong architecture, or a minimum Android version too high."
        PackageInstaller.STATUS_FAILURE_INVALID -> "Invalid: Android found $label corrupt or unsigned ($message). Rebuild and sign it."
        PackageInstaller.STATUS_FAILURE_STORAGE -> "Not enough storage to install $label ($message)."
        else -> "Failed: $label was not installed ($message)."
    }

    private fun extract(name: String, destination: String): String {
        require(name.matches(Regex("[A-Za-z][A-Za-z0-9_]*(\\.[A-Za-z0-9_]+)+"))) { "Give a package name such as org.mozilla.firefox." }
        val info = try {
            context.packageManager.getApplicationInfo(name, 0)
        } catch (_: PackageManager.NameNotFoundException) {
            error("$name is not installed (or Android hides it from 1loop).")
        }
        val target = projectFile(destination.ifEmpty { "/projects/default/apks/$name" })
        if (target.exists()) require(target.isDirectory) { "The destination is a file." }
        check(target.isDirectory || target.mkdirs()) { "The destination folder could not be created." }
        val sources = listOf(info.sourceDir) + info.splitSourceDirs.orEmpty().toList()
        val copied = sources.map { path ->
            val from = File(path)
            val to = File(target, from.name)
            from.inputStream().use { input -> to.outputStream().use { input.copyTo(it) } }
            "${to.name} (${to.length()} bytes)"
        }
        val shown = "/" + target.relativeTo(File(root() ?: target, "projects").canonicalFile.parentFile).path
        return "Extracted ${copied.size} APK(s) of $name into $shown: ${copied.joinToString(", ")}. Decompile them in Debian with apktool or jadx (see docs/INSTALL.md)."
    }
}
