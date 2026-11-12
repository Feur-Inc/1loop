// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import kotlin.math.roundToInt

/**
 * The reader's own zoom and pan over a locked browser's picture: only what
 * is shown here changes, never the page, its input or what the agent sees.
 * [x] and [y] place the zoomed picture's corner, in the view's pixels. The
 * desktop does the same (internal/ui/view_zoom.go).
 */
data class ViewZoom(val scale: Float = 1f, val x: Float = 0f, val y: Float = 0f, val url: String = "") {
    val active get() = scale > 1f
    val label get() = "${(scale * 100).roundToInt()}%"

    /** Scaled by [factor], keeping the point at ([atX], [atY]) still. */
    fun zoomAt(factor: Float, atX: Float, atY: Float, width: Float, height: Float): ViewZoom {
        val to = (scale * factor).coerceIn(1f, MAX)
        return copy(scale = to, x = atX - (atX - x) * to / scale, y = atY - (atY - y) * to / scale).clamped(width, height)
    }

    /** Moved by a drag, never past the picture's edges. */
    fun pan(dx: Float, dy: Float, width: Float, height: Float) = copy(x = x + dx, y = y + dy).clamped(width, height)

    fun clamped(width: Float, height: Float): ViewZoom =
        if (!active) reset()
        else copy(x = x.coerceIn(minOf(0f, width - width * scale), 0f), y = y.coerceIn(minOf(0f, height - height * scale), 0f))

    fun reset() = copy(scale = 1f, x = 0f, y = 0f)

    /** The page's address noted: a navigation resets the view, which follows the agent again. */
    fun follow(to: String) = if (url.isNotEmpty() && to != url) copy(url = to).reset() else copy(url = to)

    companion object {
        const val MAX = 5f
    }
}
