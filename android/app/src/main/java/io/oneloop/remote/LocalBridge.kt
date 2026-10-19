package io.oneloop.remote

import android.net.LocalServerSocket
import android.net.LocalSocket
import android.os.Process
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.io.Closeable
import java.io.DataInputStream
import java.io.DataOutputStream
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.UUID

/** Private app-UID IPC; the model receives neither this token nor its configuration. */
class LocalBridge(
    scope: CoroutineScope,
    private val phone: LocalAdb,
    private val browser: LocalBrowser,
    private val terminal: suspend (JSONObject) -> JSONObject,
    private val installPackages: suspend (JSONObject) -> JSONObject,
) : Closeable {
    private val name = "1loop-local-${UUID.randomUUID()}"
    private val token = ByteArray(32).also { SecureRandom().nextBytes(it) }.joinToString("") { "%02x".format(it.toInt() and 255) }
    private val server = LocalServerSocket(name)
    private val job = scope.launch(Dispatchers.IO) {
        while (isActive) {
            val client = try { server.accept() } catch (failure: Exception) {
                if (!isActive) break
                throw failure
            }
            launch { client.use { handle(it) } }
        }
    }

    fun configuration(): JSONObject = JSONObject().put("socket", name).put("token", token)

    private suspend fun handle(client: LocalSocket) {
        try {
            if (client.peerCredentials.uid != Process.myUid()) return
            client.soTimeout = 5000
            val input = DataInputStream(client.inputStream)
            val length = input.readInt()
            require(length in 1..256 * 1024) { "Local request is too large." }
            val bytes = ByteArray(length)
            input.readFully(bytes)
            val request = JSONObject(bytes.toString(Charsets.UTF_8))
            val supplied = request.optString("token").toByteArray(Charsets.UTF_8)
            if (!MessageDigest.isEqual(supplied, token.toByteArray(Charsets.UTF_8))) return
            val response = try {
                when (val method = request.getString("method")) {
                    "state" -> JSONObject().put("phone", phone.available()).put("browser", true)
                    "phone_use", "adb_shell" -> phone.execute(method, request.getJSONObject("arguments"))
                    "browser_native" -> browser.execute(request.getJSONObject("arguments"))
                    "terminal_native" -> terminal(request.getJSONObject("arguments"))
                    "install_packages" -> installPackages(request.getJSONObject("arguments"))
                    else -> error("Unknown local bridge method.")
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                JSONObject().put("error", failure.message ?: "Local phone action failed.")
            }
            val output = response.toString().toByteArray(Charsets.UTF_8)
            DataOutputStream(client.outputStream).apply {
                writeInt(output.size)
                write(output)
                flush()
            }
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            // Invalid input and disconnected clients must not stop the bridge.
        }
    }

    override fun close() {
        job.cancel()
        server.close()
    }
}
