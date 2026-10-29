package io.oneloop.remote

import android.net.DnsResolver
import android.os.Build
import android.os.CancellationSignal
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.withTimeout
import java.io.Closeable
import java.io.DataInputStream
import java.io.DataOutputStream
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executor
import kotlin.coroutines.resume

/** Debian uses Android's current resolver, including Private DNS and VPN policy. */
internal class LocalDns(parent: CoroutineScope) : Closeable {
    private val scope = CoroutineScope(parent.coroutineContext + SupervisorJob(parent.coroutineContext[Job]))
    private val udp = DatagramSocket(InetSocketAddress(InetAddress.getByName("127.0.0.1"), 0))
    val port = udp.localPort
    private val tcp: ServerSocket
    private val clients = ConcurrentHashMap.newKeySet<Socket>()
    private val slots = Semaphore(32)
    private val executor = Executor { task -> scope.launch { task.run() } }

    init {
        try {
            tcp = ServerSocket(port, 32, InetAddress.getByName("127.0.0.1"))
        } catch (failure: Exception) {
            udp.close()
            scope.cancel()
            throw failure
        }
        scope.launch(Dispatchers.IO) {
            try {
                val buffer = ByteArray(65535)
                while (isActive) {
                    val packet = DatagramPacket(buffer, buffer.size)
                    udp.receive(packet)
                    if (packet.length < 12 || packet.data[2].toInt() and 0x80 != 0 || !slots.tryAcquire()) continue
                    val query = packet.data.copyOf(packet.length)
                    launch {
                        try {
                            val answer = resolve(query)
                            // Small UDP answers work with every libc; larger ones retry over TCP.
                            val data = if (answer.size <= 512) answer else failed(query).apply {
                                this[2] = (this[2].toInt() or 0x02).toByte()
                                this[3] = answer[3]
                            }
                            udp.send(DatagramPacket(data, data.size, packet.socketAddress))
                        } catch (_: java.io.IOException) {
                            // The socket may close while Android completes a query.
                        }
                    }.invokeOnCompletion { slots.release() }
                }
            } catch (failure: Exception) {
                if (isActive) throw failure
            }
        }
        scope.launch(Dispatchers.IO) {
            try {
                while (isActive) {
                    val client = tcp.accept()
                    if (!slots.tryAcquire()) { client.close(); continue }
                    clients.add(client)
                    launch {
                        try {
                            client.use {
                                client.soTimeout = 5000
                                val input = DataInputStream(client.getInputStream())
                                val output = DataOutputStream(client.getOutputStream())
                                while (isActive) {
                                    val length = input.readUnsignedShort()
                                    if (length < 12) break
                                    val query = ByteArray(length)
                                    input.readFully(query)
                                    val answer = resolve(query)
                                    output.writeShort(answer.size)
                                    output.write(answer)
                                    output.flush()
                                }
                            }
                        } catch (_: java.io.IOException) {
                            // The DNS client closed or stopped reading.
                        }
                    }.invokeOnCompletion {
                        runCatching { client.close() }
                        clients.remove(client)
                        slots.release()
                    }
                }
            } catch (failure: Exception) {
                if (isActive) throw failure
            }
        }
    }

    private suspend fun resolve(query: ByteArray): ByteArray {
        try {
            return withTimeout(4000) {
                suspendCancellableCoroutine { continuation ->
                    val signal = CancellationSignal()
                    continuation.invokeOnCancellation { signal.cancel() }
                    if (Build.VERSION.SDK_INT >= 29) DnsResolver.getInstance().rawQuery(null, query, DnsResolver.FLAG_EMPTY, executor, signal,
                        object : DnsResolver.Callback<ByteArray> {
                            override fun onAnswer(answer: ByteArray, rcode: Int) {
                                if (answer.size >= 12) {
                                    answer[0] = query[0]
                                    answer[1] = query[1]
                                    continuation.resume(answer)
                                } else continuation.resume(failed(query))
                            }
                            override fun onError(error: DnsResolver.DnsException) {
                                continuation.resume(failed(query))
                            }
                        }) else continuation.resume(failed(query))
                }
            }
        } catch (cancelled: CancellationException) {
            if (!scope.isActive) throw cancelled
        } catch (_: Exception) {
            // Return a DNS failure instead of hanging or choosing a public resolver.
        }
        return failed(query)
    }

    private fun failed(query: ByteArray) = query.copyOf().apply {
        this[2] = ((this[2].toInt() and 0x79) or 0x80).toByte()
        this[3] = 0x82.toByte() // response, recursion available, SERVFAIL
        for (i in 6..9) this[i] = 0
    }

    override fun close() {
        scope.cancel()
        udp.close()
        tcp.close()
        clients.forEach { runCatching { it.close() } }
    }
}
