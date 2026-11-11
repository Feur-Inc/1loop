// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import java.io.File
import java.net.URLDecoder
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

// The same text cases as the desktop's scrubber (internal/crash/testdata).
class CrashReportTest {
    @Test
    fun sharedCases() {
        val shared = JSONObject(File("../../internal/crash/testdata/scrub_cases.json").readText())
        val names = shared.getJSONArray("names").let { array -> (0 until array.length()).map(array::getString) }
        val scrubber = Scrubber(shared.getString("home"), names)
        val cases = shared.getJSONArray("cases")
        for (i in 0 until cases.length()) {
            val case = cases.getJSONObject(i)
            assertEquals(case.getString("in"), case.getString("out"), scrubber.text(case.getString("in")))
        }
    }

    private val exception = """
        java.lang.IllegalStateException: no chat "Plan the acme launch" in /data/user/0/io.oneloop.remote/files/chats/6f1c2a9e-0b3d-4e5f-8a7b-1c2d3e4f5a6b.json
        	at io.oneloop.remote.RemoteApp.openChat(Remote.kt:912)
        	at io.oneloop.remote.ChatScreenKt${'$'}ChatScreen${'$'}1.invoke(Ui.kt:388)
        	at androidx.compose.runtime.internal.ComposableLambdaImpl.invoke(ComposableLambda.kt:109)
        Caused by: java.net.ConnectException: Failed to connect to 192.168.1.104:47101 for margot@example.org
        	at okhttp3.internal.connection.RealConnection.connect(RealConnection.kt:297)
        	... 12 more
    """.trimIndent()

    @Test
    fun trace() {
        val scrubbed = Scrubber().trace(exception)
        assertEquals("""
            java.lang.IllegalStateException: no chat "…" in <path>
            	at io.oneloop.remote.RemoteApp.openChat(Remote.kt:912)
            	at io.oneloop.remote.ChatScreenKt${'$'}ChatScreen${'$'}1.invoke(Ui.kt:388)
            	at androidx.compose.runtime.internal.ComposableLambdaImpl.invoke(ComposableLambda.kt:109)
            Caused by: java.net.ConnectException: Failed to connect to <ip> for <email>
            	at okhttp3.internal.connection.RealConnection.connect(RealConnection.kt:297)
            	... 12 more
        """.trimIndent(), scrubbed)
    }

    @Test
    fun reportAndLink() {
        val report = Report("0.9.0", "Android 16 (API 36)", "arm64-v8a", 3_720_000, "exception", Scrubber().trace(exception))
        assertEquals("Crash: java.lang.IllegalStateException: no chat \"…\" in <path>", report.title)
        assertTrue(report.body().contains("- **1loop:** phone 0.9.0, APK"))
        assertTrue(report.body().contains("- **Ran for:** 1 h 2 min"))
        val (link, cut) = report.url()
        assertFalse(cut)
        assertTrue(link.startsWith(CrashReports.NEW_ISSUE + "?title="))
        assertTrue(link.endsWith("&labels=bug"))
        val body = URLDecoder.decode(link.substringAfter("&body=").substringBefore("&labels="), "UTF-8")
        assertEquals(report.body(), body)
        assertFalse(link.contains("acme") || link.contains("margot"))
    }

    @Test
    fun longTraceIsCut() {
        val trace = "java.lang.StackOverflowError: stack size 8MB\n" + (1..600).joinToString("\n") { "\tat io.oneloop.remote.Markdown.parse(Markdown.kt:$it)" }
        val report = Report("0.9.0", "Android 16 (API 36)", "arm64-v8a", 0, "exception", trace)
        val (link, cut) = report.url()
        assertTrue(cut)
        assertTrue(link.length <= CrashReports.MAX_URL)
        assertTrue(URLDecoder.decode(link, "UTF-8").contains("more lines were cut to fit the link"))
        assertTrue(report.text.contains("Markdown.kt:600"))
    }
}
