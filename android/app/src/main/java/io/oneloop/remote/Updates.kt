package io.oneloop.remote

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageInstaller
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.util.Base64
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.core.content.ContextCompat
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
// downloaded, checked, and handed to Android's installer, which asks the
// user to confirm the update. Data stays: the same app, the same signature.

private const val MANIFEST_URL = "https://tpm28.github.io/1loop/updates/latest.json"

// The release key's public half (internal/update/update.go has the same).
private const val PUBLIC_KEY = "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAET3kQmwcR+o8F/efaAYkwc78WcTV1tO/sbBPxLH32uNqtzGn3WqfliMODCiLFoCT2dL0IrHJdBAiXQ+BXn5eSTw=="

private const val INSTALL_STATUS = "io.oneloop.remote.UPDATE_STATUS"

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
        notifier.show("update", "1loop ${release.version} is available", "Open 1loop → Settings → Updates to install it.")
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

    /** Downloads the APK, checks it, and starts Android's update screen. */
    suspend fun install(release: Release) {
        busy = true
        problem = null
        try {
            val apk = withContext(Dispatchers.IO) { download(release) }
            withContext(Dispatchers.IO) { commit(apk) }
        } catch (e: Exception) {
            problem = e.message ?: "The update could not be installed."
        } finally {
            busy = false
        }
    }

    private fun download(release: Release): File {
        val dir = File(context.cacheDir, "updates").apply { mkdirs() }
        val apk = File(dir, "1loop-${release.version}.apk")
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
            val hash = digest.digest().joinToString("") { "%02x".format(it.toInt() and 255) }
            if (size != release.size || hash != release.sha256.lowercase()) throw SecurityException("The download does not match the release: it was not installed.")
            return apk
        } catch (e: Exception) {
            apk.delete() // never keep a file that failed its check
            throw e
        } finally {
            connection.disconnect()
        }
    }

    private fun commit(apk: File) {
        val installer = context.packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            setAppPackageName(context.packageName)
        }
        val id = installer.createSession(params)
        installer.openSession(id).use { session ->
            session.openWrite("1loop.apk", 0, apk.length()).use { output ->
                apk.inputStream().use { it.copyTo(output) }
                session.fsync(output)
            }
            listenForStatus()
            val intent = Intent(INSTALL_STATUS).setPackage(context.packageName)
            val flags = PendingIntent.FLAG_UPDATE_CURRENT or (if (Build.VERSION.SDK_INT >= 31) PendingIntent.FLAG_MUTABLE else 0)
            session.commit(PendingIntent.getBroadcast(context, id, intent, flags).intentSender)
        }
    }

    private var listening = false

    /** Android answers the session: the user must confirm the update, or it failed. */
    private fun listenForStatus() {
        if (listening) return
        listening = true
        ContextCompat.registerReceiver(context, object : BroadcastReceiver() {
            override fun onReceive(receiver: Context, intent: Intent) {
                when (intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)) {
                    PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                        val confirm = if (Build.VERSION.SDK_INT >= 33) intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
                            else @Suppress("DEPRECATION") intent.getParcelableExtra(Intent.EXTRA_INTENT)
                        confirm?.let { context.startActivity(it.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
                    }
                    PackageInstaller.STATUS_SUCCESS -> Unit // Android restarts 1loop in its new version
                    else -> problem = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE) ?: "The update was not installed."
                }
            }
        }, IntentFilter(INSTALL_STATUS), ContextCompat.RECEIVER_NOT_EXPORTED)
    }
}
