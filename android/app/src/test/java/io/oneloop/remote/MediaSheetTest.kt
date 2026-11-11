// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.junit.Assert.assertEquals
import org.junit.Test

class MediaSheetTest {
    private val images = "android.permission.READ_MEDIA_IMAGES"
    private val video = "android.permission.READ_MEDIA_VIDEO"
    private val selected = "android.permission.READ_MEDIA_VISUAL_USER_SELECTED"
    private val storage = "android.permission.READ_EXTERNAL_STORAGE"

    @Test
    fun asksForWhatEachVersionUses() {
        assertEquals(listOf(images, video, selected), mediaPermissions(37).toList())
        assertEquals(listOf(images, video), mediaPermissions(33).toList())
        assertEquals(listOf(storage), mediaPermissions(29).toList())
    }

    @Test
    fun selectedPhotosOnlyIsPartialAccess() {
        assertEquals(MediaAccess.Selected, mediaAccess(37) { it == selected })
        assertEquals(MediaAccess.All, mediaAccess(37) { it == images || it == video || it == selected })
        assertEquals(MediaAccess.None, mediaAccess(37) { false })
        // Before Android 14 the partial grant does not exist.
        assertEquals(MediaAccess.None, mediaAccess(33) { it == selected })
        assertEquals(MediaAccess.All, mediaAccess(30) { it == storage })
        assertEquals(MediaAccess.None, mediaAccess(34) { it == storage })
    }

    @Test
    fun durations() {
        assertEquals("0:07", durationLabel(7_400))
        assertEquals("12:30", durationLabel(750_000))
        assertEquals("1:02:03", durationLabel(3_723_000))
    }

    @Test
    fun tappingAgainLeavesTheSelectionAndKeepsTheOrder() {
        val picked = listOf("a").toggled("b").toggled("c")
        assertEquals(listOf("a", "b", "c"), picked)
        assertEquals(listOf("a", "c"), picked.toggled("b"))
    }
}
