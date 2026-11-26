// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.io.ByteArrayInputStream

// A catalog picture is read whole only up to its bound (#525).
class WebPictureTest {
    @Test
    fun readsAPictureWithinTheBound() {
        val bytes = ByteArray(200_000) { (it % 251).toByte() }
        assertArrayEquals(bytes, readAtMost(ByteArrayInputStream(bytes), 200_000))
    }

    @Test
    fun refusesAPictureOverTheBound() {
        assertNull(readAtMost(ByteArrayInputStream(ByteArray(200_001)), 200_000))
    }

    @Test
    fun anEmptyAnswerIsEmpty() {
        assertArrayEquals(ByteArray(0), readAtMost(ByteArrayInputStream(ByteArray(0)), 10))
    }
}
