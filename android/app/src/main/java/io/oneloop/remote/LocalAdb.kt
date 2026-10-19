package io.oneloop.remote

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.graphics.BitmapFactory
import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import android.view.KeyEvent
import io.github.muntashirakon.adb.AbsAdbConnectionManager
import io.github.muntashirakon.adb.AdbConnection
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.EOFException
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.math.BigInteger
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.cert.Certificate
import java.util.Calendar
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.atomic.AtomicReference
import javax.security.auth.x500.X500Principal

/** Local loopback ADB only; its authorization is independent of agent approvals. */
class LocalAdb(private val context: Context, private val scope: CoroutineScope) {
    private val prefs = context.getSharedPreferences("1loop-local-adb", Context.MODE_PRIVATE)
    private val mutableState = MutableStateFlow(LocalAdbState(paired = prefs.getBoolean("paired", false)))
    val state = mutableState.asStateFlow()
    private val generation = AtomicLong()
    private val connection = AtomicReference<AdbConnection?>()
    private val setup = Mutex()
    private val commands = Mutex()
    private var width = 0
    private var height = 0

    suspend fun setAllowed(allowed: Boolean) {
        if (!allowed) {
            revoke()
        } else {
            generation.incrementAndGet()
            check(Build.VERSION.SDK_INT >= 30) { "Local ADB requires Android 11 or later." }
            mutableState.update { it.copy(allowed = true, problem = null) }
        }
    }

    fun revoke() {
        // Publish revocation before closing sockets or waiting on any work.
        generation.incrementAndGet()
        mutableState.value = LocalAdbState(paired = prefs.getBoolean("paired", false))
        val previous = connection.getAndSet(null)
        scope.launch(Dispatchers.IO) { runCatching { previous?.close() } }
    }

    fun available(): Boolean {
        val connected = state.value.allowed && connection.get()?.let {
            it.isConnected && it.isConnectionEstablished
        } == true
        if (state.value.connected != connected) mutableState.update { it.copy(connected = connected) }
        return connected
    }

    suspend fun pair(port: Int, code: String) {
        configure {
            require(port in 1..65535 && code.matches(Regex("[0-9]{6}"))) { "Enter the pairing port and six-digit code." }
            check(it.pair("127.0.0.1", port, code)) { "ADB pairing failed." }
        }
        prefs.edit().putBoolean("paired", true).apply()
        mutableState.update { it.copy(paired = true) }
    }

    suspend fun connect(port: Int) = configure { manager ->
        require(port in 1..65535) { "Enter a port between 1 and 65535." }
        check(manager.connect("127.0.0.1", port)) { "ADB did not connect. Check the Wireless debugging port." }
    }

    private suspend fun configure(action: (AbsAdbConnectionManager) -> Unit) = setup.withLock {
        check(state.value.allowed) { "Allow local ADB first." }
        val epoch = generation.get()
        mutableState.update { it.copy(busy = true, problem = null) }
        var manager: AbsAdbConnectionManager? = null
        try {
            withTimeout(20_000) {
                runInterruptible(Dispatchers.IO) {
                    manager = newManager()
                    action(manager!!)
                }
            }
            if (generation.get() == epoch && state.value.allowed) {
                manager!!.adbConnection?.let { next ->
                    val previous = connection.getAndSet(next)
                    withContext(Dispatchers.IO) { previous?.close() }
                }
                mutableState.update { it.copy(connected = available()) }
            } else {
                withContext(NonCancellable + Dispatchers.IO) { manager?.disconnect() }
            }
        } catch (failure: Exception) {
            withContext(NonCancellable + Dispatchers.IO) { runCatching { manager?.disconnect() } }
            if (generation.get() == epoch) mutableState.update {
                it.copy(connected = available(), problem = failure.message ?: "Local ADB setup failed.")
            }
            throw failure
        } finally {
            if (generation.get() == epoch) mutableState.update { it.copy(busy = false) }
        }
    }

    private fun newManager(): AbsAdbConnectionManager {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val alias = "1loop-local-adb"
        if (!store.containsAlias(alias)) {
            val expiry = Calendar.getInstance().apply { add(Calendar.YEAR, 10) }.time
            KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_RSA, "AndroidKeyStore").apply {
                initialize(
                    KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_SIGN or KeyProperties.PURPOSE_DECRYPT)
                        .setKeySize(2048)
                        .setDigests(KeyProperties.DIGEST_NONE, KeyProperties.DIGEST_SHA1, KeyProperties.DIGEST_SHA256, KeyProperties.DIGEST_SHA512)
                        .setSignaturePaddings(KeyProperties.SIGNATURE_PADDING_RSA_PKCS1, KeyProperties.SIGNATURE_PADDING_RSA_PSS)
                        .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                        .setCertificateSubject(X500Principal("CN=1loop local ADB"))
                        .setCertificateSerialNumber(BigInteger.ONE)
                        .setCertificateNotAfter(expiry)
                        .build(),
                )
                generateKeyPair()
            }
        }
        val key = store.getKey(alias, null) as PrivateKey
        val certificate = store.getCertificate(alias)
        return object : AbsAdbConnectionManager() {
            override fun getPrivateKey(): PrivateKey = key
            override fun getCertificate(): Certificate = certificate
            override fun getDeviceName(): String = "1loop local phone"
        }.apply {
            setApi(Build.VERSION.SDK_INT)
            setHostAddress("127.0.0.1")
            setTimeout(10, TimeUnit.SECONDS)
            setThrowOnUnauthorised(true)
        }
    }

    private fun requireConnection(): AdbConnection {
        check(available()) { "Local ADB is disabled or disconnected." }
        return connection.get() ?: error("Local ADB is disconnected.")
    }

    private suspend fun command(command: String, limit: Int = 256 * 1024): AdbShellResult = withTimeout(60_000) {
        runInterruptible(Dispatchers.IO) {
            val current = requireConnection()
            current.open("shell,v2,raw:$command").use { stream ->
                // Shell v2 has an explicit exit packet. This avoids the ADB
                // library's close/read race and keeps screenshots binary-safe.
                fun readFully(bytes: ByteArray) {
                    var offset = 0
                    while (offset < bytes.size) {
                        val count = stream.read(bytes, offset, bytes.size - offset)
                        if (count <= 0) throw EOFException("ADB ended before the command completed.")
                        offset += count
                    }
                }
                val result = readAdbShellResult(limit, ::readFully)
                check(state.value.allowed) { "Local ADB permission was revoked." }
                if (result.exitCode != 0) {
                    val diagnostic = (result.stderr.toString(Charsets.UTF_8) + result.stdout.toString(Charsets.UTF_8)).take(4096)
                    error("ADB command exited with status ${result.exitCode}." + if (diagnostic.isBlank()) "" else "\n$diagnostic")
                }
                result
            }
        }
    }

    suspend fun execute(name: String, arguments: JSONObject): JSONObject = commands.withLock {
        requireConnection()
        if (name == "adb_shell") {
            val command = arguments.getString("command")
            require(command.isNotBlank() && command.length <= 32_768) { "Provide an Android shell command up to 32768 characters." }
            val result = this.command(command)
            return@withLock JSONObject().put("text", result.stdout.toString(Charsets.UTF_8) + result.stderr.toString(Charsets.UTF_8))
        }
        require(name == "phone_use") { "Unknown local phone tool." }
        val text = arguments.optString("text")
        when (arguments.getString("action")) {
            "state", "screenshot" -> {
                val png = command("screencap -p", 32 * 1024 * 1024).stdout
                val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
                BitmapFactory.decodeByteArray(png, 0, png.size, bounds)
                check(bounds.outWidth > 0 && bounds.outHeight > 0) { "ADB did not return a valid screenshot." }
                width = bounds.outWidth
                height = bounds.outHeight
                return@withLock JSONObject()
                    .put("text", "Display: $width x $height pixels. Coordinates refer to this image.")
                    .put("images", JSONArray().put(JSONObject().put("mime", "image/png").put("data", Base64.encodeToString(png, Base64.NO_WRAP))))
            }
            "tap" -> {
                val (x, y) = point(arguments, "x", "y")
                command("input tap $x $y")
            }
            "swipe" -> {
                val (x, y) = point(arguments, "x", "y")
                val (endX, endY) = point(arguments, "end_x", "end_y")
                val duration = arguments.optInt("duration_ms").takeIf { it > 0 } ?: 500
                require(duration in 1..10_000) { "Swipe duration must be between 1 and 10000 milliseconds." }
                command("input swipe $x $y $endX $endY $duration")
            }
            "type" -> {
                require(text.isNotEmpty() && text.length <= 32_768) { "Provide text up to 32768 characters." }
                // Clipboard paste preserves Unicode and newlines; `input text`
                // silently changes some characters and cannot type Unicode.
                withContext(Dispatchers.Main) {
                    requireConnection()
                    context.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("1loop", text))
                }
                command("input keyevent ${KeyEvent.KEYCODE_PASTE}")
            }
            "key" -> {
                val key = KeyEvent.keyCodeFromString(if (text.startsWith("KEYCODE_")) text else "KEYCODE_$text")
                require(key in 1..KeyEvent.getMaxKeyCode()) { "Provide a valid Android key name, such as BACK or ENTER." }
                command("input keyevent $key")
            }
            else -> error("Unknown Phone Use action.")
        }
        JSONObject().put("text", "Phone action completed. Read a fresh screenshot before the next interaction.")
    }

    private fun point(arguments: JSONObject, xKey: String, yKey: String): Pair<Int, Int> {
        val x = arguments.getInt(xKey)
        val y = arguments.getInt(yKey)
        require(width > 0 && height > 0) { "Read a fresh phone screenshot first." }
        require(x in 0 until width && y in 0 until height) { "Coordinates are outside the last screenshot." }
        return x to y
    }
}

internal data class AdbShellResult(val stdout: ByteArray, val stderr: ByteArray, val exitCode: Int)

// Shell-v2 separates binary stdout from diagnostics and sends one unsigned exit byte.
internal fun readAdbShellResult(limit: Int, readFully: (ByteArray) -> Unit): AdbShellResult {
    val stdout = ByteArrayOutputStream()
    val stderr = ByteArrayOutputStream()
    while (true) {
        val header = ByteArray(5)
        readFully(header)
        val length = ByteBuffer.wrap(header, 1, 4).order(ByteOrder.LITTLE_ENDIAN).int
        val kind = header[0].toInt() and 0xff
        if (kind == 3) {
            check(length == 1) { "Invalid ADB exit status packet." }
            val status = ByteArray(1)
            readFully(status)
            return AdbShellResult(stdout.toByteArray(), stderr.toByteArray(), status[0].toInt() and 0xff)
        }
        check(kind == 1 || kind == 2) { "Unexpected ADB shell packet." }
        check(length >= 0 && length <= limit - stdout.size() - stderr.size()) {
            "ADB output exceeds the limit. Filter or page the command output."
        }
        val payload = ByteArray(length)
        readFully(payload)
        (if (kind == 1) stdout else stderr).write(payload)
    }
}
