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
import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.concurrent.ConcurrentHashMap
import javax.net.ssl.HostnameVerifier
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocketFactory
import javax.net.ssl.X509TrustManager

// The desktop side is phone_server.go: UDP discovery on 47100, HTTPS on 47101.
const val DISCOVERY_PORT = 47100
const val DEFAULT_PORT = 47101

/** desktop: a 1loop window found on the network or by its QR code (else a headless server). */
data class Server(val name: String, val host: String, val port: Int, val token: String = "", val scheme: String = "https", val fingerprint: String = "", val localPhone: Boolean = false, val desktop: Boolean = false) {
    val key get() = if (localPhone) "local-phone" else if (scheme == "http") "$host:$port" else "$scheme://$host:$port"
    val baseURL get() = "$scheme://${if (host.contains(':')) "[${host.trim('[', ']')}]" else host}:$port"
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
                // Older desktops answer over plain HTTP: they are not listed until updated.
                val fingerprint = reply.optString("fingerprint").lowercase()
                if (reply.optString("service") == "1loop" && reply.optString("scheme") == "https" && fingerprint.matches(FINGERPRINT)) {
                    val server = Server(reply.optString("name", "1loop"), packet.address.hostAddress ?: return@runCatching, reply.optInt("port", DEFAULT_PORT), fingerprint = fingerprint, desktop = true)
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

private val FINGERPRINT = Regex("[0-9a-f]{64}")

/**
 * The token a computer found on the network keeps: the one of the known
 * computer with the same certificate. A broadcast answer proves nothing, so
 * a name or an address alone never gets a token.
 */
fun carriedToken(known: List<Server>, found: Server): Server? =
    known.firstOrNull { it.desktop && it.paired && it.fingerprint.isNotEmpty() && it.fingerprint == found.fingerprint }

/** The start of a fingerprint, as the computer shows it during pairing. */
fun fingerprintCheck(fingerprint: String): String = fingerprint.take(16).uppercase().chunked(4).joinToString(" ")

/** What a desktop's pairing QR code carries (phone_server.go, showPairingQR). */
data class PairingLink(val name: String, val hosts: List<String>, val port: Int, val fingerprint: String, val pairing: String, val secret: String)

fun parsePairingLink(text: String): PairingLink? = runCatching {
    val uri = java.net.URI(text.trim())
    if (uri.scheme != "oneloop" || uri.host != "pair") return null
    val query = uri.rawQuery.orEmpty().split('&').filter { '=' in it }.associate {
        val (key, value) = it.split('=', limit = 2)
        java.net.URLDecoder.decode(key, "UTF-8") to java.net.URLDecoder.decode(value, "UTF-8")
    }
    val fingerprint = query["f"].orEmpty().lowercase()
    val hosts = query["h"].orEmpty().split(',').map { it.trim() }.filter { it.isNotEmpty() }
    val port = query["p"]?.toIntOrNull() ?: return null
    if (query["v"] != "1" || !fingerprint.matches(FINGERPRINT) || hosts.isEmpty() || port !in 1..65535 ||
        query["i"].isNullOrEmpty() || query["s"].orEmpty().length != 32) return null
    PairingLink(query["n"].orEmpty().ifEmpty { "1loop" }, hosts, port, fingerprint, query["i"]!!, query["s"]!!)
}.getOrNull()

/** The certificate fingerprint a computer presents, read without trusting it: the user checks it while pairing. */
fun presentedFingerprint(host: String, port: Int): String {
    val trustAll = object : X509TrustManager {
        override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) = throw CertificateException("Client certificates are not used")
        override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {}
    }
    val factory = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trustAll), null) }.socketFactory
    (factory.createSocket() as javax.net.ssl.SSLSocket).use { socket ->
        socket.connect(java.net.InetSocketAddress(host, port), 4000)
        socket.soTimeout = 4000
        socket.startHandshake()
        return fingerprintOf(socket.session.peerCertificates.first() as X509Certificate)
    }
}

// A headless server (SERVER.md) and a desktop have private certificates:
// the phone trusts them by their SHA-256 fingerprint, and nothing else.

private val pinnedFactories = ConcurrentHashMap<String, SSLSocketFactory>()

private fun fingerprintOf(certificate: X509Certificate): String =
    MessageDigest.getInstance("SHA-256").digest(certificate.encoded).joinToString("") { "%02x".format(it.toInt() and 255) }

private fun pinnedSocketFactory(expected: String): SSLSocketFactory = pinnedFactories.computeIfAbsent(expected) {
    val trust = object : X509TrustManager {
        override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {
            throw CertificateException("Client certificates are not used")
        }
        override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
            if (chain.isEmpty() || fingerprintOf(chain[0]) != expected) {
                throw CertificateException("Server certificate fingerprint changed. Verify it on the server console.")
            }
            chain[0].checkValidity()
        }
    }
    SSLContext.getInstance("TLS").apply { init(null, arrayOf(trust), null) }.socketFactory
}

private fun pin(connection: HttpsURLConnection, fingerprint: String) {
    val expected = fingerprint.replace(":", "").lowercase()
    require(expected.matches(Regex("[0-9a-f]{64}"))) { "Enter the SHA-256 fingerprint from the server console." }
    connection.sslSocketFactory = pinnedSocketFactory(expected)
    connection.hostnameVerifier = HostnameVerifier { _, session ->
        (session.peerCertificates.firstOrNull() as? X509Certificate)?.let(::fingerprintOf) == expected
    }
}

/** One computer's API. Every call is blocking: run it on Dispatchers.IO. */
class Api(val server: Server) {
    private val base = server.baseURL

    private fun connection(path: String): HttpURLConnection {
        // The token never travels in clear text: plain HTTP only to this phone itself.
        if (server.scheme != "https" && server.host !in listOf("127.0.0.1", "localhost", "::1")) throw ApiError("${server.name} needs a secure connection. Pair it again.")
        val connection = URL(base + path).openConnection() as HttpURLConnection
        connection.instanceFollowRedirects = false
        if (connection is HttpsURLConnection && server.fingerprint.isNotEmpty()) pin(connection, server.fingerprint)
        if (server.token.isNotEmpty()) connection.setRequestProperty("Authorization", "Bearer ${server.token}")
        return connection
    }

    fun get(path: String): JSONObject = call("GET", path, null)

    /** Raw bytes, as a browser tab's picture. */
    fun bytes(path: String): ByteArray {
        val connection = connection(path).apply {
            connectTimeout = 4000
            readTimeout = 20000
        }
        try {
            if (connection.responseCode !in 200..299) throw ApiError("1loop answered ${connection.responseCode}", connection.responseCode)
            return connection.inputStream.use { it.readBytes() }
        } catch (e: ApiError) {
            throw e
        } catch (e: Exception) {
            throw ApiError("Cannot reach ${server.name} (${server.host}).")
        } finally {
            connection.disconnect()
        }
    }
    /** readTimeout: 20 s by default; longer only for a call known to wait (a browser's first start). */
    fun post(path: String, body: JSONObject, readTimeout: Int = 20000): JSONObject = call("POST", path, body, readTimeout)

    /** A file as the request body, streamed (attachments, up to 64 MiB). */
    fun upload(path: String, input: java.io.InputStream, length: Long): JSONObject {
        val connection = connection(path).apply {
            requestMethod = "POST"
            connectTimeout = 4000
            readTimeout = 120000
            doOutput = true
            setRequestProperty("Content-Type", "application/octet-stream")
            if (length >= 0) setFixedLengthStreamingMode(length) else setChunkedStreamingMode(1 shl 16)
        }
        try {
            connection.outputStream.use { input.copyTo(it, 1 shl 16) }
            val status = connection.responseCode
            val stream = if (status in 200..299) connection.inputStream else connection.errorStream
            val json = runCatching { JSONObject(stream?.bufferedReader()?.use { it.readText() }.orEmpty()) }.getOrDefault(JSONObject())
            if (status !in 200..299) throw ApiError(json.optString("error", "1loop answered $status"), status)
            return json
        } catch (e: ApiError) {
            throw e
        } catch (e: Exception) {
            throw ApiError("Cannot send the file to ${server.name}.")
        } finally {
            connection.disconnect()
        }
    }

    private fun call(method: String, path: String, body: JSONObject?, timeout: Int = 20000): JSONObject {
        val connection = connection(path).apply {
            requestMethod = method
            connectTimeout = 4000
            readTimeout = timeout
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
     * Follows a binary stream of messages (a kind byte, a big-endian length,
     * the payload) until it ends or fails; see browser_phone.go.
     */
    suspend fun messages(path: String, onMessage: suspend (Char, ByteArray) -> Unit) = withContext(Dispatchers.IO) {
        val connection = connection(path).apply {
            connectTimeout = 4000
            readTimeout = 40000 // the computer sends the state every 15 s
        }
        try {
            if (connection.responseCode !in 200..299) throw ApiError("1loop answered ${connection.responseCode}", connection.responseCode)
            java.io.DataInputStream(java.io.BufferedInputStream(connection.inputStream, 1 shl 16)).use { input ->
                while (true) {
                    currentCoroutineContext().ensureActive()
                    val kind = input.readUnsignedByte().toChar()
                    val length = input.readInt()
                    require(length in 0..(32 shl 20)) { "Invalid browser frame size" }
                    val payload = ByteArray(length)
                    input.readFully(payload)
                    onMessage(kind, payload)
                }
            }
        } finally {
            connection.disconnect()
        }
    }

    /**
     * Follows the server-sent events until the stream ends or fails; each
     * event reaches onEvent with its name and JSON data.
     */
    suspend fun events(onOpen: () -> Unit, onEvent: (String, JSONObject) -> Unit) = withContext(Dispatchers.IO) {
        val connection = connection("/api/events").apply {
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
