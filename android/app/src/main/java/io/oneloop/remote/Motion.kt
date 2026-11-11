// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.content.Context
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.snap
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.core.content.edit

/**
 * The app's animations switch (Settings, General: Animations). Compose
 * already ends every animation at once while the system's "Remove
 * animations" is on; this is the app's own way to the same, for the
 * animations that pass their spec through [motion].
 */
object Motion {
    private const val PREFERENCES = "1loop-motion"
    private const val OFF = "off"

    var enabled by mutableStateOf(true)
        private set

    fun load(context: Context) {
        enabled = !context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).getBoolean(OFF, false)
    }

    fun set(context: Context, on: Boolean) {
        enabled = on
        context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).edit { putBoolean(OFF, !on) }
    }
}

/** [spec] while animations are on, an immediate change when they are off. */
fun <T> motion(spec: FiniteAnimationSpec<T>): FiniteAnimationSpec<T> = if (Motion.enabled) spec else snap()
