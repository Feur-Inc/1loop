// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.activity.BackEventCompat
import androidx.activity.compose.PredictiveBackHandler
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.spring
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import kotlin.coroutines.cancellation.CancellationException

/**
 * Back for a full-screen page, with Android's predictive back (#254): while
 * the user swipes back, the page shrinks a little and follows the finger
 * toward the edge it came from; let go, it goes back (onBack); cancelled,
 * it settles in place. In place of BackHandler: put the returned modifier
 * first on the page's root.
 */
@Composable
fun predictiveBack(enabled: Boolean = true, onBack: () -> Unit): Modifier {
    val progress = remember { Animatable(0f) }
    val edge = remember { floatArrayOf(1f) } // 1: swiped from the left edge, -1: from the right
    val scope = rememberCoroutineScope()
    PredictiveBackHandler(enabled) { events ->
        try {
            events.collect { event ->
                edge[0] = if (event.swipeEdge == BackEventCompat.EDGE_LEFT) 1f else -1f
                progress.snapTo(event.progress)
            }
            onBack()
            progress.snapTo(0f)
        } catch (cancelled: CancellationException) {
            scope.launch { progress.animateTo(0f, spring()) }
            throw cancelled
        }
    }
    return Modifier.graphicsLayer {
        val p = progress.value
        if (p > 0f) {
            val scale = 1f - 0.1f * p
            scaleX = scale
            scaleY = scale
            translationX = edge[0] * p * 16.dp.toPx()
            shape = RoundedCornerShape(28.dp * p)
            clip = true
        }
    }
}
