package io.oneloop.remote

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.Settings
import android.util.Base64
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.core.content.FileProvider
import androidx.core.content.pm.PackageInfoCompat
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.security.KeyFactory
import java.security.MessageDigest
import java.security.Signature
import java.security.spec.X509EncodedKeySpec

// The phone app's updates (the desktop's internal/update): the release
// page's signed manifest says what the latest version is; the APK is
// downloaded, checked against it, and opened in Android's own installer,
// which asks the user to confirm and runs Play Protect's checks itself.
// Data stays: the same app, the same signature (Android refuses another).
// An in-app PackageInstaller session stalled there: Play Protect's dialog
// outlives the install verification timeout and its answer is ignored.

private const val MANIFEST_URL = "https://tpm28.github.io/1loop/updates/latest.json"

// The release key's public half (internal/update/update.go has the same).
private const val PUBLIC_KEY = "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAET3kQmwcR+o8F/efaAYkwc78WcTV1tO/sbBPxLH32uNqtzGn3WqfliMODCiLFoCT2dL0IrHJdBAiXQ+BXn5eSTw=="

data class Release(val version: String, val code: Int, val notes: String, val url: String, val sha256: String, val size: Long)

class Updates(private val context: Context) {
    private val prefs = context.getSharedPreferences("1loop-updates", Context.MODE_PRIVATE)

    var latest by mutableStateOf<Release?>(null)
        private set
    var problem by mutableStateOf<String?>(null)
    var busy by mutableStateOf(false)
        private set
    var checkedAt by mutableStateOf(prefs.getLong("checked", 0L))
        private set

    val currentVersion: String = context.packageManager.getPackageInfo(context.packageName, 0).versionName.orEmpty()
    private val currentCode: Long = PackageInfoCompat.getLongVersionCode(context.packageManager.getPackageInfo(context.packageName, 0))

    val available get() = latest?.let { it.code > currentCode } == true

    /** A checked APK waiting for the "install unknown apps" permission. */
    private var pending: File? = null

    init {
        // Sessions left by 0.2 and 0.3, whose in-app install could stall.
        runCatching { context.packageManager.packageInstaller.let { installer -> installer.mySessions.forEach { installer.abandonSession(it.sessionId) } } }
    }

    /** Reads the release page; returns the release when it is newer than this app. */
    suspend fun check(): Release? = withContext(Dispatchers.IO) {
        try {
            val connection = (URL(MANIFEST_URL).openConnection() as HttpURLConnection).apply {
                connectTimeout = 10000
                readTimeout = 20000
                useCaches = false
            }
            val text = try {
                if (connection.responseCode != 200) throw IllegalStateException("The release page answered ${connection.responseCode}")
                connection.inputStream.bufferedReader().use { it.readText() }
            } finally {
                connection.disconnect()
            }
            val release = verify(text)
            latest = release
            problem = null
            checkedAt = System.currentTimeMillis()
            prefs.edit().putLong("checked", checkedAt).apply()
            release.takeIf { it.code > currentCode }
        } catch (e: Exception) {
            problem = e.message ?: "Cannot reach the release page."
            null
        }
    }

    /** At most every six hours: a notification once per new version. */
    suspend fun checkInBackground(notifier: Notifier) {
        if (System.currentTimeMillis() - checkedAt < 6 * 3600_000L) return
        val release = check() ?: return
        if (prefs.getString("notified", null) == release.version) return
        prefs.edit().putString("notified", release.version).apply()
        notifier.showUpdate("1loop ${release.version} is available", "Tap to install it. Your chats and settings stay.")
    }

    private fun verify(text: String): Release {
        val envelope = JSONObject(text)
        val payload = Base64.decode(envelope.getString("payload"), Base64.DEFAULT)
        val signature = Base64.decode(envelope.getString("signature"), Base64.DEFAULT)
        val key = KeyFactory.getInstance("EC").generatePublic(X509EncodedKeySpec(Base64.decode(PUBLIC_KEY, Base64.DEFAULT)))
        val verifier = Signature.getInstance("SHA256withECDSA").apply { initVerify(key); update(payload) }
        if (!verifier.verify(signature)) throw SecurityException("The update manifest is not signed by 1loop's release key.")
        val manifest = JSONObject(String(payload))
        val apk = manifest.getJSONObject("files").optJSONObject("android") ?: throw IllegalStateException("This release has no Android app.")
        return Release(manifest.getString("version"), manifest.getInt("code"), manifest.optString("notes"), apk.getString("url"), apk.getString("sha256"), apk.getLong("size"))
    }

    /** Android asks once whether 1loop may install apps: its update. */
    fun mayInstall() = context.packageManager.canRequestPackageInstalls()

    fun openInstallPermission() {
        context.startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${context.packageName}")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }

    /** Downloads the APK, checks it, and opens Android's installer on it. */
    suspend fun install(release: Release) {
        busy = true
        problem = null
        try {
            val apk = withContext(Dispatchers.IO) { download(release) }
            if (mayInstall()) openInstaller(apk) else {
                pending = apk
                problem = "Allow 1loop to install apps (its updates) on the page that opens: the update continues when you come back."
                openInstallPermission()
            }
        } catch (e: Exception) {
            problem = e.message ?: "The update could not be installed."
        } finally {
            busy = false
        }
    }

    /** Back from the permission page: the waiting update goes on. */
    fun resume() {
        val apk = pending ?: return
        if (!mayInstall()) return
        pending = null
        problem = null
        runCatching { openInstaller(apk) }.onFailure { problem = it.message ?: "The update could not be installed." }
    }

    private fun openInstaller(apk: File) {
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.updates", apk)
        context.startActivity(
            Intent(Intent.ACTION_VIEW).setDataAndType(uri, "application/vnd.android.package-archive")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK),
        )
    }

    private fun download(release: Release): File {
        val dir = File(context.cacheDir, "updates").apply { mkdirs() }
        val apk = File(dir, "1loop-${release.version}.apk")
        dir.listFiles()?.filter { it != apk }?.forEach { it.delete() } // earlier versions' downloads
        if (apk.length() == release.size && sha256(apk) == release.sha256.lowercase()) return apk // checked before the permission page
        val connection = (URL(release.url).openConnection() as HttpURLConnection).apply {
            connectTimeout = 10000
            readTimeout = 60000
        }
        try {
            if (connection.responseCode != 200) throw IllegalStateException("Download failed: ${connection.responseCode}")
            val digest = MessageDigest.getInstance("SHA-256")
            var size = 0L
            connection.inputStream.use { input ->
                apk.outputStream().use { output ->
                    val buffer = ByteArray(1 shl 16)
                    while (true) {
                        val read = input.read(buffer)
                        if (read < 0) break
                        size += read
                        if (size > release.size) throw IllegalStateException("The download is larger than the release says.")
                        digest.update(buffer, 0, read)
                        output.write(buffer, 0, read)
                    }
                }
            }
            val hash = hex(digest.digest())
            if (size != release.size || hash != release.sha256.lowercase()) throw SecurityException("The download does not match the release: it was not installed.")
            return apk
        } catch (e: Exception) {
            apk.delete() // never keep a file that failed its check
            throw e
        } finally {
            connection.disconnect()
        }
    }

    private fun sha256(file: File): String {
        val digest = MessageDigest.getInstance("SHA-256")
        file.inputStream().use { input ->
            val buffer = ByteArray(1 shl 16)
            while (true) {
                val read = input.read(buffer)
                if (read < 0) break
                digest.update(buffer, 0, read)
            }
        }
        return hex(digest.digest())
    }

    private fun hex(bytes: ByteArray) = bytes.joinToString("") { "%02x".format(it.toInt() and 255) }
}
