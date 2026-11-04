package io.oneloop.remote

import android.annotation.SuppressLint
import android.content.AttributionSource
import android.content.Context
import android.content.ContextWrapper
import android.graphics.Bitmap
import android.graphics.PixelFormat
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.ImageReader
import android.os.Binder
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import android.os.IBinder
import android.os.ParcelFileDescriptor
import androidx.annotation.RequiresApi
import androidx.annotation.Keep
import java.io.ByteArrayOutputStream
import java.io.DataOutputStream
import java.io.InputStream
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

/** Runs as Shizuku's shell UID. Large screenshots travel through a pipe, not Binder parcels. */
@Keep
class PhoneShellService(private val context: Context) : IPhoneShell.Stub() {
    private val owner = context.applicationInfo.uid
    private val active = AtomicReference<Process?>()
    private val hidden = Any()
    private var display: VirtualDisplay? = null
    private var reader: ImageReader? = null
    private var frames: HandlerThread? = null
    private var size = 0 to 0
    @Volatile private var frame: Bitmap? = null

    override fun execute(command: String, limit: Int): ParcelFileDescriptor {
        check(Binder.getCallingUid() == owner) { "Unexpected phone service caller." }
        require(command.isNotBlank() && command.length <= 32768 && limit in 1..(32 * 1024 * 1024))
        val pipe = ParcelFileDescriptor.createPipe()
        Thread({
            val readers = Executors.newFixedThreadPool(2)
            var process: Process? = null
            try {
                DataOutputStream(ParcelFileDescriptor.AutoCloseOutputStream(pipe[1])).use { output ->
                    var status = -1
                    var stdout = byteArrayOf()
                    var stderr = byteArrayOf()
                    try {
                        process = ProcessBuilder("/system/bin/sh", "-c", command).start()
                        check(active.compareAndSet(null, process)) { "A phone command is already running." }
                        val budget = AtomicInteger(limit)
                        fun read(stream: InputStream): ByteArray = stream.use {
                            val bytes = ByteArrayOutputStream()
                            val buffer = ByteArray(8192)
                            while (true) {
                                val count = it.read(buffer)
                                if (count < 0) break
                                check(budget.addAndGet(-count) >= 0) { "Phone command output exceeds the limit. Filter or page it." }
                                bytes.write(buffer, 0, count)
                            }
                            bytes.toByteArray()
                        }
                        val child = process!!
                        val out = readers.submit<ByteArray> { read(child.inputStream) }
                        val err = readers.submit<ByteArray> { read(child.errorStream) }
                        check(child.waitFor(50, TimeUnit.SECONDS)) { "Phone command timed out." }
                        stdout = out.get(2, TimeUnit.SECONDS)
                        stderr = err.get(2, TimeUnit.SECONDS)
                        status = child.exitValue()
                    } catch (failure: Exception) {
                        stdout = byteArrayOf()
                        stderr = (failure.message ?: "Phone command failed.").toByteArray().take(limit).toByteArray()
                    }
                    output.writeInt(status)
                    output.writeInt(stdout.size); output.write(stdout)
                    output.writeInt(stderr.size); output.write(stderr)
                }
            } catch (_: Exception) {
                // Revocation or a disconnected client closes the receiving pipe.
            } finally {
                process?.destroyForcibly()
                active.compareAndSet(process, null)
                readers.shutdownNow()
            }
        }, "1loop-phone-command").start()
        return pipe[0]
    }

    /**
     * The hidden session: a virtual display nobody sees, drawn into an
     * ImageReader. It is created as the shell, like scrcpy's --new-display:
     * it keeps its own focus, so typing there leaves the user's screen alone.
     */
    override fun openHiddenDisplay(width: Int, height: Int, density: Int): Int {
        check(Binder.getCallingUid() == owner) { "Unexpected phone service caller." }
        require(width in 1..8192 && height in 1..8192 && density in 1..2000) { "Invalid hidden display size." }
        synchronized(hidden) { return display?.display?.displayId ?: createHiddenDisplay(width, height, density) }
    }

    private fun createHiddenDisplay(width: Int, height: Int, density: Int): Int {
        val thread = HandlerThread("1loop-hidden-display").apply { start() }
        val images = ImageReader.newInstance(width, height, PixelFormat.RGBA_8888, 2)
        images.setOnImageAvailableListener({ source ->
            source.acquireLatestImage()?.use { image ->
                val plane = image.planes[0]
                val padded = Bitmap.createBitmap(plane.rowStride / plane.pixelStride, image.height, Bitmap.Config.ARGB_8888)
                padded.copyPixelsFromBuffer(plane.buffer)
                frame = if (padded.width == image.width) padded else Bitmap.createBitmap(padded, 0, 0, image.width, image.height)
            }
        }, Handler(thread.looper))
        return try {
            // DisplayManager checks that the package matches the calling UID.
            val manager = DisplayManager::class.java.getDeclaredConstructor(Context::class.java)
                .apply { isAccessible = true }.newInstance(ShellContext(context))
            val created = manager.createVirtualDisplay("1loop hidden session", width, height, density, images.surface, hiddenFlags())
                ?: error("Android refused to create the hidden display.")
            hideKeyboard(created.display.displayId)
            display = created
            reader = images
            frames = thread
            size = width to height
            created.display.displayId
        } catch (failure: Exception) {
            images.close()
            thread.quitSafely()
            throw IllegalStateException("The hidden session is unavailable on this phone: " + (failure.cause ?: failure).message, failure)
        }
    }

    override fun captureHiddenDisplay(): ParcelFileDescriptor {
        check(Binder.getCallingUid() == owner) { "Unexpected phone service caller." }
        val (width, height) = synchronized(hidden) { check(display != null) { "Open the hidden session first." }; size }
        // A display that never drew is black.
        val image = frame ?: Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888)
        val pipe = ParcelFileDescriptor.createPipe()
        Thread({
            runCatching { ParcelFileDescriptor.AutoCloseOutputStream(pipe[1]).use { image.compress(Bitmap.CompressFormat.PNG, 100, it) } }
        }, "1loop-hidden-capture").start()
        return pipe[0]
    }

    override fun closeHiddenDisplay() {
        check(Binder.getCallingUid() == owner) { "Unexpected phone service caller." }
        releaseHiddenDisplay()
    }

    private fun releaseHiddenDisplay() = synchronized(hidden) {
        display?.release()
        reader?.close()
        frames?.quitSafely()
        display = null
        reader = null
        frames = null
        frame = null
    }

    private fun hiddenFlags(): Int {
        // DisplayManager's public, then hidden, VIRTUAL_DISPLAY_FLAG_* values.
        var flags = DisplayManager.VIRTUAL_DISPLAY_FLAG_PUBLIC or DisplayManager.VIRTUAL_DISPLAY_FLAG_OWN_CONTENT_ONLY or
            (1 shl 6) /* SUPPORTS_TOUCH */ or (1 shl 8) /* DESTROY_CONTENT_ON_REMOVAL */
        if (Build.VERSION.SDK_INT >= 33) flags = flags or (1 shl 10) /* TRUSTED */ or (1 shl 11) /* OWN_DISPLAY_GROUP */ or
            (1 shl 12) /* ALWAYS_UNLOCKED */ or (1 shl 13) /* TOUCH_FEEDBACK_DISABLED */
        if (Build.VERSION.SDK_INT >= 34) flags = flags or (1 shl 14) /* OWN_FOCUS */ or (1 shl 15) /* DEVICE_DISPLAY_GROUP */
        return flags
    }

    /** Typing pastes, so no keyboard is needed; otherwise it would open on the user's screen. */
    @SuppressLint("PrivateApi", "BlockedPrivateApi")
    private fun hideKeyboard(displayId: Int) {
        if (Build.VERSION.SDK_INT < 31) return
        runCatching {
            val binder = Class.forName("android.os.ServiceManager").getMethod("getService", String::class.java).invoke(null, "window")
            val manager = Class.forName("android.view.IWindowManager\$Stub").getMethod("asInterface", IBinder::class.java).invoke(null, binder)!!
            manager.javaClass.getMethod("setDisplayImePolicy", Int::class.javaPrimitiveType, Int::class.javaPrimitiveType)
                .invoke(manager, displayId, 2 /* DISPLAY_IME_POLICY_HIDE */)
        }
    }

    override fun destroy() {
        active.getAndSet(null)?.destroyForcibly()
        releaseHiddenDisplay()
        System.exit(0)
    }
}

/** Identifies the hidden display's creator as the shell, whose UID this process has. */
private class ShellContext(base: Context) : ContextWrapper(base) {
    override fun getPackageName() = "com.android.shell"
    override fun getOpPackageName() = "com.android.shell"

    @RequiresApi(31)
    override fun getAttributionSource(): AttributionSource =
        AttributionSource.Builder(android.os.Process.myUid()).setPackageName("com.android.shell").build()
}
