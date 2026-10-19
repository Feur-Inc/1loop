package io.oneloop.remote

import android.content.Context
import android.os.Binder
import android.os.ParcelFileDescriptor
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
class PhoneShellService(context: Context) : IPhoneShell.Stub() {
    private val owner = context.applicationInfo.uid
    private val active = AtomicReference<Process?>()

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

    override fun destroy() {
        active.getAndSet(null)?.destroyForcibly()
        System.exit(0)
    }
}
