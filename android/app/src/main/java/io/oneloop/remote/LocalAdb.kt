package io.oneloop.remote

import android.content.ClipData
import android.content.ClipboardManager
import android.content.ComponentName
import android.content.Context
import android.content.ServiceConnection
import android.content.pm.PackageManager
import android.graphics.BitmapFactory
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.util.Base64
import android.view.KeyEvent
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import org.json.JSONArray
import org.json.JSONObject
import rikka.shizuku.Shizuku
import java.io.DataInputStream
import java.util.concurrent.atomic.AtomicLong

/** Shizuku owns the local shell connection; 1loop's grant is independent. */
class LocalAdb(private val context: Context, private val scope: CoroutineScope) {
    private val mutableState = MutableStateFlow(LocalAdbState())
    val state = mutableState.asStateFlow()
    private val generation = AtomicLong()
    private val commands = Mutex()
    @Volatile private var shell: IPhoneShell? = null
    @Volatile private var binding: ServiceConnection? = null
    private var width = 0
    private var height = 0
    private val serviceArgs = Shizuku.UserServiceArgs(ComponentName(context, PhoneShellService::class.java))
        .tag("1loop-phone-shell").version(1).daemon(false).processNameSuffix("phone")

    init {
        Shizuku.addBinderReceivedListenerSticky {
            refresh()
            if (state.value.allowed) scope.launch { connectService() }
        }
        Shizuku.addBinderDeadListener {
            generation.incrementAndGet()
            shell = null
            binding = null
            refresh()
        }
        Shizuku.addRequestPermissionResultListener { request, result ->
            if (request == 4101) {
                if (result == PackageManager.PERMISSION_GRANTED && state.value.allowed) scope.launch { connectService() }
                else revoke()
            }
        }
        refresh()
    }

    fun refresh() {
        val installed = runCatching { context.packageManager.getPackageInfo("moe.shizuku.privileged.api", 0) }.isSuccess
        val running = Shizuku.pingBinder()
        val connected = running && runCatching { Shizuku.checkSelfPermission() == PackageManager.PERMISSION_GRANTED }.getOrDefault(false) && shell?.asBinder()?.isBinderAlive == true
        mutableState.update { it.copy(installed = installed, serviceRunning = running, connected = it.allowed && connected, busy = if (running) it.busy else false) }
    }

    suspend fun setAllowed(allowed: Boolean) {
        if (!allowed) { revoke(); return }
        withContext(Dispatchers.Main) {
            mutableState.update { it.copy(allowed = true, problem = null) }
            refresh()
            if (!state.value.serviceRunning) {
                mutableState.update { it.copy(problem = "Install and start Shizuku first, then return to 1loop.") }
            } else if (Shizuku.checkSelfPermission() == PackageManager.PERMISSION_GRANTED) {
                connectService()
            } else {
                Shizuku.requestPermission(4101)
            }
        }
    }

    private suspend fun connectService() = withContext(Dispatchers.Main) {
        if (!state.value.allowed || !Shizuku.pingBinder()) return@withContext
        if (Shizuku.checkSelfPermission() != PackageManager.PERMISSION_GRANTED) return@withContext
        if (shell != null || binding != null) return@withContext
        val epoch = generation.incrementAndGet()
        val connection = object : ServiceConnection {
            override fun onServiceConnected(name: ComponentName, binder: IBinder) {
                if (epoch != generation.get() || !state.value.allowed) return
                shell = IPhoneShell.Stub.asInterface(binder)
                mutableState.update { it.copy(busy = false, problem = null) }
                refresh()
            }
            override fun onServiceDisconnected(name: ComponentName) {
                if (epoch != generation.get()) return
                shell = null
                binding = null
                mutableState.update { it.copy(connected = false, busy = false) }
            }
        }
        binding = connection
        mutableState.update { it.copy(busy = true) }
        try { Shizuku.bindUserService(serviceArgs, connection) }
        catch (failure: Exception) {
            binding = null
            mutableState.update { it.copy(busy = false, problem = failure.message ?: "Shizuku could not start the phone service.") }
        }
    }

    fun revoke() {
        generation.incrementAndGet()
        mutableState.update { it.copy(allowed = false, connected = false, busy = false, problem = null) }
        shell = null
        binding?.let { connection ->
            // Detach the client's cached connection before killing the service:
            // an immediate rebind must not reuse the dying Binder callback.
            runCatching { Shizuku.unbindUserService(serviceArgs, connection, false) }
            runCatching { Shizuku.unbindUserService(serviceArgs, null, true) }
        }
        binding = null
    }

    fun available(): Boolean { refresh(); return state.value.allowed && state.value.connected }

    private fun requireAccess(): IPhoneShell {
        check(available()) { "Phone Use is disabled or Shizuku is unavailable." }
        return shell ?: error("Shizuku is disconnected.")
    }

    private suspend fun command(command: String, limit: Int = 256 * 1024): PhoneShellResult = withTimeout(60_000) {
        runInterruptible(Dispatchers.IO) {
            val epoch = generation.get()
            val descriptor = requireAccess().execute(command, limit)
            DataInputStream(ParcelFileDescriptor.AutoCloseInputStream(descriptor)).use { input ->
                val status = input.readInt()
                fun readBytes(maximum: Int): ByteArray {
                    val count = input.readInt()
                    check(count in 0..maximum) { "Invalid Shizuku command output." }
                    return ByteArray(count).also { input.readFully(it) }
                }
                val stdout = readBytes(limit)
                val stderr = readBytes(limit - stdout.size)
                check(epoch == generation.get() && available()) { "Phone Use permission was revoked." }
                check(status == 0) { "Android command exited with status $status.\n" + (stderr.toString(Charsets.UTF_8) + stdout.toString(Charsets.UTF_8)).take(4096) }
                PhoneShellResult(stdout, stderr)
            }
        }
    }

    suspend fun execute(name: String, arguments: JSONObject): JSONObject = commands.withLock {
        requireAccess()
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
                    requireAccess()
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

internal data class PhoneShellResult(val stdout: ByteArray, val stderr: ByteArray)
