// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class MarkdownLinkTest {
    @Test
    fun externalLinksAllowOnlySafeSchemes() {
        assertEquals("https://example.com/docs", safeExternalLink("https://example.com/docs"))
        assertEquals("http://localhost:8080", safeExternalLink("  http://localhost:8080  "))
        assertEquals("mailto:test@example.com", safeExternalLink("mailto:test@example.com"))
        assertEquals("tel:+33123456789", safeExternalLink("tel:+33123456789"))
        assertEquals("HTTPS://EXAMPLE.COM", safeExternalLink("HTTPS://EXAMPLE.COM"))

        assertNull(safeExternalLink("file:///home/user/notes.md"))
        assertNull(safeExternalLink("FILE:///C:/Users/notes.md"))
        assertNull(safeExternalLink("content://io.oneloop.remote/files/a.txt"))
        assertNull(safeExternalLink("intent://scan/#Intent;end"))
        assertNull(safeExternalLink("javascript:alert(1)"))
        assertNull(safeExternalLink("relative/path.md"))
        assertNull(safeExternalLink(""))
    }

    @Test
    fun fileLinksParseIntoCitations() {
        assertEquals(
            Citation("/home/user/notes.md", 0, 0),
            fileLinkCitation("file:///home/user/notes.md"),
        )
        assertEquals(
            Citation("/home/user/notes.md", 42, 42),
            fileLinkCitation("file:///home/user/notes.md#L42"),
        )
        assertEquals(
            Citation("/home/user/notes.md", 10, 25),
            fileLinkCitation("file:///home/user/notes.md#L10-L25"),
        )
        assertEquals(
            Citation("/home/user/my notes.md", 3, 7),
            fileLinkCitation("file:///home/user/my%20notes.md#L3-7"),
        )
        assertNull(fileLinkCitation("https://example.com"))
        assertNull(fileLinkCitation("file://"))
    }
}
