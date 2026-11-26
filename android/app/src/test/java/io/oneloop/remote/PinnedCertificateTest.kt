// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import java.security.cert.CertificateException
import java.security.cert.CertificateExpiredException
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate

// The server's self-signed certificate is never renewed (#517): the phone
// keeps trusting the one it pinned past its dates, and nothing else.
class PinnedCertificateTest {
    // Self-signed, valid from 2024-01-01 to 2025-01-01 only.
    private val expired = CertificateFactory.getInstance("X.509").generateCertificate(
        """
        -----BEGIN CERTIFICATE-----
        MIIBhDCCASmgAwIBAgIUYwWrEyAfZvytQm6i0MCRF8q1AecwCgYIKoZIzj0EAwIw
        FzEVMBMGA1UEAwwMMWxvb3Agc2VydmVyMB4XDTI0MDEwMTAwMDAwMFoXDTI1MDEw
        MTAwMDAwMFowFzEVMBMGA1UEAwwMMWxvb3Agc2VydmVyMFkwEwYHKoZIzj0CAQYI
        KoZIzj0DAQcDQgAEHN6l3h3+WIH061kSUosodsXgRQMDbpLrnzkyj2IyrMzJsAEp
        IfI97aqFOyLwinj9mESVvSKg5zKdjh2bMp8gaKNTMFEwHQYDVR0OBBYEFGVaKRMn
        RYxNrJJv88YLFR4zblOXMB8GA1UdIwQYMBaAFGVaKRMnRYxNrJJv88YLFR4zblOX
        MA8GA1UdEwEB/wQFMAMBAf8wCgYIKoZIzj0EAwIDSQAwRgIhAMTUaYTvOpdDmW3c
        yBEhfsicDh7FGn5bJtN7nPq5D82YAiEAgLKwzuICiOdORhVsvxOtrbNgr6GEeuSA
        jiPzSoHGdCY=
        -----END CERTIFICATE-----
        """.trimIndent().byteInputStream(),
    ) as X509Certificate

    @Test
    fun pinsTheFingerprintTheServerPrints() {
        // openssl x509 -fingerprint -sha256, as the server console shows it.
        assertEquals("0c1a635d273ef71a15162b6f5a403182fc7861b51dbd311b9a1540186271eaff", fingerprintOf(expired))
    }

    @Test
    fun acceptsThePinnedCertificatePastItsDates() {
        assertThrows(CertificateExpiredException::class.java) { expired.checkValidity() }
        pinnedTrust(fingerprintOf(expired)).checkServerTrusted(arrayOf(expired), "ECDHE_ECDSA")
    }

    @Test
    fun refusesAnyOtherCertificate() {
        assertThrows(CertificateException::class.java) { pinnedTrust("cd".repeat(32)).checkServerTrusted(arrayOf(expired), "ECDHE_ECDSA") }
        assertThrows(CertificateException::class.java) { pinnedTrust(fingerprintOf(expired)).checkServerTrusted(emptyArray(), "ECDHE_ECDSA") }
    }
}
