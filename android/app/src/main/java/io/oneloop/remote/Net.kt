package io.oneloop.remote

import android.content.Context
import android.net.ConnectivityManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.BufferedReader
import java.io.InputStreamReader
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.HttpURLConnection
import java.net.Inet4Address
import java.net.InetAddress
import java.net.SocketTimeoutException
import java.net.URL
import java.net.URLEncoder

// The desktop side is remote.go: UDP discovery on 47100, HTTP on 47101.
const val DISCOVERY_PORT = 47100
const val DEFAULT_PORT = 47101

data class Server(val name: String, val host: String, val port: Int, val token: String = "") {
    val key get() = "$host:$port"
    val paired get() = token.isNotEmpty()
}

class ApiError(message: String, val status: Int = 0) : Exception(message)

/**
 * Finds the 1loop computers on the network: a "1loop?" broadcast, to the
 * whole network and to the Wi-Fi subnet, then the answers for a moment.
 */
suspend fun discover(context: Context, listenMs: Int = 1600): List<Server> = withContext(Dispatchers.IO) {
    val found = linkedMapOf<String, Server>()
    DatagramSocket().use { socket ->
        socket.broadcast = true
        socket.soTimeout = 250
        val ask = "1loop?".toByteArray()
        val targets = (listOf(InetAddress.getByName("255.255.255.255")) + subnetBroadcasts(context)).distinct()
        repeat(2) { // a second ask covers a dropped packet
            for (target in targets) runCatching { socket.send(DatagramPacket(ask, ask.size, target, DISCOVERY_PORT)) }
        }
        val buf = ByteArray(1024)
        val until = System.currentTimeMillis() + listenMs
        while (System.currentTimeMillis() < until) {
            currentCoroutineContext().ensureActive()
            val packet = DatagramPacket(buf, buf.size)
            try {
                socket.receive(packet)
            } catch (_: SocketTimeoutException) {
                continue
            }
            runCatching {
                val reply = JSONObject(String(packet.data, 0, packet.length))
                if (reply.optString("service") == "1loop") {
                    val server = Server(reply.optString("name", "1loop"), packet.address.hostAddress ?: return@runCatching, reply.optInt("port", DEFAULT_PORT))
                    found[server.key] = server
                }
            }
        }
    }
    found.values.toList()
}

// The broadcast address of each IPv4 network the phone is on (Wi-Fi first).
private fun subnetBroadcasts(context: Context): List<InetAddress> {
    val manager = context.getSystemService(ConnectivityManager::class.java) ?: return emptyList()
    val network = manager.activeNetwork ?: return emptyList()
    val links = manager.getLinkProperties(network) ?: return emptyList()
    return links.linkAddresses.mapNotNull { link ->
        val address = link.address as? Inet4Address ?: return@mapNotNull null
        val bytes = address.address
        val mask = if (link.prefixLength == 0) 0 else -1 shl (32 - link.prefixLength)
        val ip = bytes.fold(0) { acc, b -> (acc shl 8) or (b.toInt() and 0xFF) }
        val broadcast = ip or mask.inv()
        InetAddress.getByAddress(ByteArray(4) { i -> (broadcast shr (24 - 8 * i)).toByte() })
    }
}

/** One computer's API. Every call is blocking: run it on Dispatchers.IO. */
class Api(val server: Server) {
    private val base = "http://${server.host}:${server.port}"

    fun get(path: String): JSONObject = call("GET", path, null)
    fun post(path: String, body: JSONObject): JSONObject = call("POST", path, body)

    private fun call(method: String, path: String, body: JSONObject?): JSONObject {
        val connection = (URL(base + path).openConnection() as HttpURLConnection).apply {
            requestMethod = method
            connectTimeout = 4000
            readTimeout = 20000
            if (server.token.isNotEmpty()) setRequestProperty("Authorization", "Bearer ${server.token}")
            if (body != null) {
                doOutput = true
                setRequestProperty("Content-Type", "application/json")
            }
        }
        try {
            if (body != null) connection.outputStream.use { it.write(body.toString().toByteArray()) }
            val status = connection.responseCode
            val stream = if (status in 200..299) connection.inputStream else connection.errorStream
            val text = stream?.bufferedReader()?.use { it.readText() }.orEmpty()
            val json = runCatching { JSONObject(text) }.getOrDefault(JSONObject())
            if (status !in 200..299) throw ApiError(json.optString("error", "1loop answered $status"), status)
            return json
        } catch (e: ApiError) {
            throw e
        } catch (e: Exception) {
            throw ApiError("Cannot reach ${server.name} (${server.host}).")
        } finally {
            connection.disconnect()
        }
    }

    /**
     * Follows the server-sent events until the stream ends or fails; each
     * event reaches onEvent with its name and JSON data.
     */
    suspend fun events(onOpen: () -> Unit, onEvent: (String, JSONObject) -> Unit) = withContext(Dispatchers.IO) {
        val token = URLEncoder.encode(server.token, "UTF-8")
        val connection = (URL("$base/api/events?token=$token").openConnection() as HttpURLConnection).apply {
            connectTimeout = 4000
            readTimeout = 45000 // the computer pings every 20 s
            setRequestProperty("Accept", "text/event-stream")
        }
        try {
            if (connection.responseCode == 401) throw ApiError("This phone is no longer paired.", 401)
            if (connection.responseCode != 200) throw ApiError("1loop answered ${connection.responseCode}")
            onOpen()
            BufferedReader(InputStreamReader(connection.inputStream)).use { reader ->
                var name = "message"
                val data = StringBuilder()
                while (true) {
                    currentCoroutineContext().ensureActive()
                    val line = reader.readLine() ?: break
                    when {
                        line.isEmpty() -> {
                            if (data.isNotEmpty()) runCatching { JSONObject(data.toString()) }.getOrNull()?.let { onEvent(name, it) }
                            name = "message"
                            data.clear()
                        }
                        line.startsWith("event:") -> name = line.removePrefix("event:").trim()
                        line.startsWith("data:") -> data.append(line.removePrefix("data:").trim())
                    }
                }
            }
        } finally {
            connection.disconnect()
        }
    }
}
