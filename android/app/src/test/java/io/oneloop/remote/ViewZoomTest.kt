// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ViewZoomTest {
    private val w = 400f
    private val h = 300f

    @Test
    fun keepsThePinchCentreStill() {
        val zoom = ViewZoom().zoomAt(2f, 100f, 60f, w, h)
        assertTrue(zoom.active)
        assertEquals("200%", zoom.label)
        // the point under the fingers, (100, 60) unzoomed, is still there
        assertEquals(100f, zoom.x + 100f * zoom.scale, 0.01f)
        assertEquals(60f, zoom.y + 60f * zoom.scale, 0.01f)
    }

    @Test
    fun staysWithinThePicture() {
        var zoom = ViewZoom().zoomAt(100f, 0f, 0f, w, h)
        assertEquals(ViewZoom.MAX, zoom.scale, 0f)
        zoom = zoom.pan(500f, 500f, w, h)
        assertEquals(0f, zoom.x, 0f)
        assertEquals(0f, zoom.y, 0f)
        zoom = zoom.pan(-1e6f, -1e6f, w, h)
        assertEquals(w - w * ViewZoom.MAX, zoom.x, 0.01f)
        assertEquals(h - h * ViewZoom.MAX, zoom.y, 0.01f)
        zoom = zoom.zoomAt(0.01f, 10f, 10f, w, h)
        assertFalse(zoom.active)
        assertEquals(ViewZoom(url = zoom.url), zoom)
    }

    @Test
    fun anUnzoomedDragMovesNothing() {
        assertEquals(ViewZoom(), ViewZoom().pan(40f, -40f, w, h))
    }

    @Test
    fun followsTheAgentWhenItNavigates() {
        var zoom = ViewZoom().follow("https://example.com/").zoomAt(3f, 200f, 150f, w, h)
        zoom = zoom.follow("https://example.com/")
        assertTrue("the same page keeps the zoom", zoom.active)
        zoom = zoom.follow("https://example.com/next")
        assertFalse("a navigation resets the view", zoom.active)
        assertEquals("https://example.com/next", zoom.url)
        assertFalse(zoom.zoomAt(2f, 0f, 0f, w, h).reset().active)
    }
}
