package io.oneloop.remote

import android.content.ClipData
import android.content.ClipboardManager
import android.content.ComponentName
import android.content.Context
import android.content.ServiceConnection
import android.content.pm.PackageManager
import android.graphics.BitmapFactory
import android.hardware.display.DisplayManager
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.util.Base64
import android.util.DisplayMetrics
import android.view.Display
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

/**
 * Shizuku owns the local shell connection; 1loop's grant is independent. The
 * user's choice is saved: a restart of 1loop reconnects by itself while
 * Shizuku runs and still authorizes 1loop, and a revoke stays revoked.
 */
class LocalAdb(private val context: Context, private val scope: CoroutineScope) {
    private val prefs = context.getSharedPreferences("1loop-local-adb", Context.MODE_PRIVATE)
    private val mutableState = MutableStateFlow(LocalAdbState(allowed = prefs.getBoolean("allowed", false)))
    val state = mutableState.asStateFlow()
    private val generation = AtomicLong()
    private val commands = Mutex()
    @Volatile private var shell: IPhoneShell? = null
    @Volatile private var binding: ServiceConnection? = null
    // Last screenshot size of each session: the user's screen and the hidden display.
    private val sizes = mutableMapOf<Boolean, Pair<Int, Int>>()
    // The local agent is stopped: no connection, the user's choice kept (pause, resume).
    @Volatile private var suspended = true
    private val serviceArgs = Shizuku.UserServiceArgs(ComponentName(context, PhoneShellService::class.java))
        .tag("1loop-phone-shell").version(2).daemon(false).processNameSuffix("phone")

    init {
        Shizuku.addBinderReceivedListenerSticky {
            refresh()
            if (state.value.allowed && !suspended) scope.launch { connectService() }
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
                else revoke() // denied in Shizuku's dialog: the user's answer
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
            suspended = false
            save(true)
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

    /** The local agent starts: reconnect when the user allowed it before. */
    suspend fun resume() {
        suspended = false
        withContext(Dispatchers.Main) { refresh() }
        if (state.value.allowed) connectService()
    }

    /** The local agent stops: disconnect without forgetting the user's choice. */
    fun pause() {
        suspended = true
        disconnect()
    }

    private fun save(allowed: Boolean) {
        check(prefs.edit().putBoolean("allowed", allowed).commit()) { "Unable to save the Phone Use choice." }
    }

    private suspend fun connectService() = withContext(Dispatchers.Main) {
        if (suspended || !state.value.allowed || !Shizuku.pingBinder()) return@withContext
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

    /** The user turns Phone Use off: saved, so a restart does not turn it back on. */
    fun revoke() {
        generation.incrementAndGet()
        mutableState.update { it.copy(allowed = false, connected = false, busy = false, problem = null) }
        runCatching { save(false) }
        disconnect()
    }

    private fun disconnect() {
        generation.incrementAndGet()
        mutableState.update { it.copy(connected = false, busy = false) }
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

    /** The hidden display's ID, created at the phone's portrait size on first use. */
    private suspend fun hiddenDisplay(): Int = withTimeout(30_000) {
        val metrics = DisplayMetrics()
        @Suppress("DEPRECATION")
        context.getSystemService(DisplayManager::class.java).getDisplay(Display.DEFAULT_DISPLAY).getRealMetrics(metrics)
        runInterruptible(Dispatchers.IO) {
            requireAccess().openHiddenDisplay(minOf(metrics.widthPixels, metrics.heightPixels), maxOf(metrics.widthPixels, metrics.heightPixels), metrics.densityDpi)
        }
    }

    private suspend fun screenshot(hidden: Boolean): ByteArray {
        if (!hidden) return command("screencap -p", 32 * 1024 * 1024).stdout
        hiddenDisplay()
        return withTimeout(30_000) {
            runInterruptible(Dispatchers.IO) {
                ParcelFileDescriptor.AutoCloseInputStream(requireAccess().captureHiddenDisplay()).use { it.readBytes() }
            }
        }
    }

    /** Managed (work) profiles first, then the user's own account. */
    private suspend fun launchUsers(hidden: Boolean): List<Int> {
        val current = command("am get-current-user").stdout.toString(Charsets.UTF_8).trim().toInt()
        if (!hidden) return listOf(current)
        val profiles = Regex("""UserInfo\{(\d+):[^:}]*:([0-9a-fA-F]+)\}""").findAll(command("pm list users").stdout.toString(Charsets.UTF_8))
            .filter { it.groupValues[2].toInt(16) and 0x20 != 0 }.map { it.groupValues[1].toInt() }.toList()
        return profiles + current
    }

    private suspend fun launch(packageName: String, hidden: Boolean): String {
        require(Regex("""[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+""").matches(packageName)) { "Provide the app's package name in text, such as org.mozilla.firefox." }
        val display = if (hidden) hiddenDisplay() else null
        for (user in launchUsers(hidden)) {
            val component = command("cmd package resolve-activity --brief --user $user -a android.intent.action.MAIN -c android.intent.category.LAUNCHER $packageName")
                .stdout.toString(Charsets.UTF_8).trim().lines().last()
            if ('/' !in component) continue
            // A new task keeps an app the user has open on their screen there.
            val target = if (display != null) "--display $display -f 0x18000000" else "-f 0x10000000"
            val output = command("am start --user $user $target -n $component").let { it.stdout.toString(Charsets.UTF_8) + it.stderr.toString(Charsets.UTF_8) }
            check("Error" !in output) { output.trim().take(4096) }
            return "Opened $component for Android user $user" + (if (hidden) " in the hidden session." else " on the user's screen.") + " Read a fresh screenshot before the next interaction."
        }
        error("$packageName has no launcher activity" + if (hidden) " in a work profile or the user's account." else ".")
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
        val hidden = when (arguments.optString("session").ifEmpty { "user" }) {
            "user" -> false
            "hidden" -> true
            else -> error("session must be user or hidden.")
        }
        suspend fun input() = if (hidden) "input -d ${hiddenDisplay()}" else "input"
        when (arguments.getString("action")) {
            "state", "screenshot" -> {
                val png = screenshot(hidden)
                val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
                BitmapFactory.decodeByteArray(png, 0, png.size, bounds)
                check(bounds.outWidth > 0 && bounds.outHeight > 0) { "ADB did not return a valid screenshot." }
                sizes[hidden] = bounds.outWidth to bounds.outHeight
                val where = if (hidden) "Hidden session (display ${hiddenDisplay()}, invisible to the user)" else "User's screen"
                return@withLock JSONObject()
                    .put("text", "$where: ${bounds.outWidth} x ${bounds.outHeight} pixels. Coordinates refer to this image.")
                    .put("images", JSONArray().put(JSONObject().put("mime", "image/png").put("data", Base64.encodeToString(png, Base64.NO_WRAP))))
            }
            "tap" -> {
                val (x, y) = point(arguments, hidden, "x", "y")
                command("${input()} tap $x $y")
            }
            "swipe" -> {
                val (x, y) = point(arguments, hidden, "x", "y")
                val (endX, endY) = point(arguments, hidden, "end_x", "end_y")
                val duration = arguments.optInt("duration_ms").takeIf { it > 0 } ?: 500
                require(duration in 1..10_000) { "Swipe duration must be between 1 and 10000 milliseconds." }
                command("${input()} swipe $x $y $endX $endY $duration")
            }
            "type" -> {
                require(text.isNotEmpty() && text.length <= 32_768) { "Provide text up to 32768 characters." }
                // Clipboard paste preserves Unicode and newlines; `input text`
                // silently changes some characters and cannot type Unicode.
                withContext(Dispatchers.Main) {
                    requireAccess()
                    context.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("1loop", text))
                }
                command("${input()} keyevent ${KeyEvent.KEYCODE_PASTE}")
            }
            "key" -> {
                val key = KeyEvent.keyCodeFromString(if (text.startsWith("KEYCODE_")) text else "KEYCODE_$text")
                require(key in 1..KeyEvent.getMaxKeyCode()) { "Provide a valid Android key name, such as BACK or ENTER." }
                command("${input()} keyevent $key")
            }
            "launch" -> return@withLock JSONObject().put("text", launch(text.trim(), hidden))
            "close" -> {
                require(hidden) { "Only the hidden session can be closed." }
                runInterruptible(Dispatchers.IO) { requireAccess().closeHiddenDisplay() }
                sizes.remove(true)
                return@withLock JSONObject().put("text", "Closed the hidden session and the apps it showed.")
            }
            else -> error("Unknown Phone Use action.")
        }
        JSONObject().put("text", "Phone action completed. Read a fresh screenshot before the next interaction.")
    }

    private fun point(arguments: JSONObject, hidden: Boolean, xKey: String, yKey: String): Pair<Int, Int> {
        val x = arguments.getInt(xKey)
        val y = arguments.getInt(yKey)
        val (width, height) = sizes[hidden] ?: error("Read a fresh screenshot of this session first.")
        require(x in 0 until width && y in 0 until height) { "Coordinates are outside the last screenshot." }
        return x to y
    }
}

internal data class PhoneShellResult(val stdout: ByteArray, val stderr: ByteArray)
