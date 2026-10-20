package io.oneloop.remote

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class PairingTest {
    private val fingerprint = "ab".repeat(32)
    private val secret = "0123456789abcdef0123456789abcdef"

    @Test
    fun parsesTheDesktopQrCode() {
        val link = parsePairingLink("oneloop://pair?f=$fingerprint&h=192.168.1.20%2C10.0.0.5&i=abc&n=My+PC&p=47101&s=$secret&v=1")
        assertEquals(PairingLink("My PC", listOf("192.168.1.20", "10.0.0.5"), 47101, fingerprint, "abc", secret), link)
    }

    @Test
    fun rejectsIncompleteOrForeignCodes() {
        assertNull(parsePairingLink("https://example.com/?f=$fingerprint"))
        assertNull(parsePairingLink("oneloop://pair?f=$fingerprint&h=1.2.3.4&i=abc&p=47101&s=short&v=1"))
        assertNull(parsePairingLink("oneloop://pair?f=nothex&h=1.2.3.4&i=abc&p=47101&s=$secret&v=1"))
        assertNull(parsePairingLink("oneloop://pair?f=$fingerprint&h=&i=abc&p=47101&s=$secret&v=1"))
        assertNull(parsePairingLink("oneloop://pair?f=$fingerprint&h=1.2.3.4&i=abc&p=99999&s=$secret&v=1"))
        assertNull(parsePairingLink("not a uri at all"))
    }

    @Test
    fun aTokenFollowsTheCertificateNotTheName() {
        val known = Server("My PC", "192.168.1.20", 47101, token = "secret-token", fingerprint = fingerprint, desktop = true)
        // Same name and port, another certificate: a forged discovery answer.
        val forged = Server("My PC", "192.168.1.66", 47101, fingerprint = "cd".repeat(32), desktop = true)
        assertNull(carriedToken(listOf(known), forged))
        // Same certificate at a new address: the same computer.
        val moved = Server("My PC", "192.168.1.21", 47101, fingerprint = fingerprint, desktop = true)
        assertEquals(known, carriedToken(listOf(known), moved))
    }

    @Test
    fun fingerprintCheckMatchesTheDesktop() {
        assertEquals("ABAB ABAB ABAB ABAB", fingerprintCheck(fingerprint))
    }
}
