package io.oneloop.remote

import android.animation.ValueAnimator
import android.annotation.SuppressLint
import android.content.Context
import android.graphics.PixelFormat
import android.graphics.Rect
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.HapticFeedbackConstants
import android.view.MotionEvent
import android.view.VelocityTracker
import android.view.View
import android.view.ViewConfiguration
import android.view.WindowInsets
import android.view.WindowManager
import android.view.animation.DecelerateInterpolator
import android.widget.FrameLayout
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.geometry.Offset
import androidx.core.content.edit
import androidx.lifecycle.findViewTreeLifecycleOwner
import androidx.lifecycle.setViewTreeLifecycleOwner
import androidx.savedstate.findViewTreeSavedStateRegistryOwner
import androidx.savedstate.setViewTreeSavedStateRegistryOwner
import kotlin.math.abs
import kotlin.math.hypot
import kotlin.math.roundToInt

/** What the badge's content (StatusBadge) and its window (BadgeWindow) share. */
class BadgeUi {
    var onRight by mutableStateOf(true) // on the right half: the pill opens toward the left
    var dragging by mutableStateOf(false) // the pill folds while the badge moves
    var targetShown by mutableStateOf(false) // the remove target at the bottom
    var nearTarget by mutableStateOf(false) // the badge sits on it: let go to remove
    var expanded = false // the pill is open
    var handle = androidx.compose.ui.geometry.Rect.Zero // where a drag may start, in the window
}

/**
 * The badge's overlay window: it goes anywhere the finger takes it, and
 * settles on the nearest side when let go close to it or flung toward it.
 * While it moves, a ring with an ✕ shows at the bottom of the screen: let go
 * on it and the badge goes away (onRemove).
 *
 * The drag reads the touches' screen coordinates in a frame around the
 * content: positions inside a window that moves under the finger shift with
 * it, and would make the badge shake.
 */
class BadgeWindow(
    private val context: Context,
    private val compose: (@Composable () -> Unit) -> View,
    private val onRemove: () -> Unit,
    badge: @Composable (BadgeUi) -> Unit,
) {
    val ui = BadgeUi()
    private val manager = context.getSystemService(WindowManager::class.java)
    private val preferences = context.getSharedPreferences("agent_status", Context.MODE_PRIVATE)
    private val handler = Handler(Looper.getMainLooper())
    private val density = context.resources.displayMetrics.density
    private fun px(dp: Float) = (dp * density).roundToInt()

    // The collapsed window: 8 dp of padding around a 52 dp badge, its ring in the middle.
    private val half = px(34f)
    private val frame = Frame(context).apply {
        val content = compose { badge(ui) }
        addView(content)
        // The window's root: Compose looks for its owners there, and finds those of the
        // content (the service) only on the content itself.
        setViewTreeLifecycleOwner(content.findViewTreeLifecycleOwner())
        setViewTreeSavedStateRegistryOwner(content.findViewTreeSavedStateRegistryOwner())
    }
    private val params = WindowManager.LayoutParams(
        WindowManager.LayoutParams.WRAP_CONTENT, WindowManager.LayoutParams.WRAP_CONTENT,
        WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
        // Hardware accelerated: a window added from a service is drawn in software
        // otherwise, and the working ring animates every frame.
        // Not touch modal: touches beside the badge reach the app below, even while it
        // takes the keyboard (typing an answer).
        // In screen: its position is in the touches' screen coordinates.
        WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_LAYOUT_NO_LIMITS or
            WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN or WindowManager.LayoutParams.FLAG_HARDWARE_ACCELERATED or
            WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL,
        PixelFormat.TRANSLUCENT,
    ).apply { cutout(this) }
    private var attached = false

    // At rest: the window's distance from its side (onRight: the right one) and from the top.
    private var anchorX = preferences.getInt(X, 0)
    private var anchorY = preferences.getInt(Y, (screen().height() * 0.28f).toInt())
    private var snapping: ValueAnimator? = null

    init {
        ui.onRight = preferences.getBoolean(RIGHT, true)
        // The pill opening or a question growing it: kept on screen.
        frame.addOnLayoutChangeListener { _, left, top, right, bottom, oldLeft, oldTop, oldRight, oldBottom ->
            if (right - left != oldRight - oldLeft || bottom - top != oldBottom - oldTop) frame.post { place() }
        }
    }

    /** Shows the window, or applies phone use (click-through, translucent) and the keyboard. */
    fun update(passThrough: Boolean, focusable: Boolean) {
        if (passThrough && ui.dragging) finish(0f, cancelled = true)
        val through = WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE
        val notFocusable = WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE
        params.flags = if (passThrough) params.flags or through else params.flags and through.inv()
        params.flags = if (focusable) params.flags and notFocusable.inv() else params.flags or notFocusable
        // At most 0.8 opaque, or Android blocks the touches it lets through.
        params.alpha = if (passThrough) 0.4f else 1f
        if (!attached) {
            rest()
            manager.addView(frame, params)
            attached = true
        } else apply()
    }

    fun remove() {
        snapping?.cancel()
        ui.dragging = false
        dropTarget.run()
        velocity?.recycle()
        velocity = null
        if (attached) runCatching { manager.removeView(frame) }
        attached = false
    }

    /** The screen turned or changed size: back on it. */
    fun place() {
        if (!attached || ui.dragging) return
        rest()
        apply()
    }

    private fun apply() {
        if (attached) runCatching { manager.updateViewLayout(frame, params) }
    }

    // The resting position, kept on screen with the window's current size.
    private fun rest() {
        val screen = screen()
        val safe = safe()
        val width = frame.width.takeIf { it > 0 } ?: (2 * half)
        val height = frame.height.takeIf { it > 0 } ?: (2 * half)
        params.gravity = Gravity.TOP or if (ui.onRight) Gravity.END else Gravity.START
        params.x = anchorX.coerceAtMost(screen.width() - width).coerceAtLeast(0)
        params.y = anchorY.coerceAtMost(safe.bottom - height + px(8f)).coerceAtLeast(safe.top - px(8f))
    }

    // -- dragging -------------------------------------------------------------

    private val slop = ViewConfiguration.get(context).scaledTouchSlop
    private var armed = false
    private var downX = 0f
    private var downY = 0f
    private var grabX = 0f // from the finger to the ring's centre
    private var grabY = 0f
    private var ringX = 0f // the ring's centre on screen, while it moves
    private var ringY = 0f
    private var velocity: VelocityTracker? = null

    @SuppressLint("ClickableViewAccessibility") // a drag, never a click: the content keeps those
    private inner class Frame(context: Context) : FrameLayout(context) {
        override fun onInterceptTouchEvent(event: MotionEvent) = touch(event, intercepting = true)
        override fun onTouchEvent(event: MotionEvent) = touch(event, intercepting = false)
    }

    private fun touch(event: MotionEvent, intercepting: Boolean): Boolean {
        when (event.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                // Only from the badge or the pill: the question card keeps its touches.
                armed = !ui.dragging && ui.handle.contains(Offset(event.x, event.y))
                if (!armed) return false
                velocity?.clear()
                downX = event.rawX
                downY = event.rawY
                if (ui.expanded) {
                    // The pill folds into the badge, under the finger.
                    grabX = 0f
                    grabY = 0f
                } else {
                    val at = IntArray(2)
                    frame.getLocationOnScreen(at)
                    grabX = downX - (at[0] + ui.handle.center.x)
                    grabY = downY - (at[1] + ui.handle.center.y)
                }
                track(event)
                return !intercepting
            }
            MotionEvent.ACTION_MOVE -> {
                if (!armed) return false
                track(event)
                if (!ui.dragging && hypot(event.rawX - downX, event.rawY - downY) > slop) start()
                if (ui.dragging) move(event.rawX, event.rawY)
                return ui.dragging
            }
            MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                if (!armed) return false
                armed = false
                track(event)
                if (!ui.dragging) return false
                val tracker = velocity
                tracker?.computeCurrentVelocity(1000)
                finish(tracker?.xVelocity ?: 0f, cancelled = event.actionMasked == MotionEvent.ACTION_CANCEL)
                return true
            }
        }
        return ui.dragging
    }

    // In screen coordinates: those of a window that moves would move with it.
    private fun track(event: MotionEvent) {
        val copy = MotionEvent.obtain(event)
        copy.setLocation(event.rawX, event.rawY)
        (velocity ?: VelocityTracker.obtain().also { velocity = it }).addMovement(copy)
        copy.recycle()
    }

    private fun start() {
        snapping?.cancel()
        ui.dragging = true
        params.gravity = Gravity.TOP or Gravity.START
        showTarget()
    }

    private fun move(rawX: Float, rawY: Float) {
        val safe = safe()
        val ring = px(26f)
        ringX = (rawX - grabX).coerceIn((safe.left + ring).toFloat(), (safe.right - ring).toFloat())
        ringY = (rawY - grabY).coerceIn((safe.top + ring).toFloat(), (safe.bottom - ring).toFloat())
        // The target pulls the badge in when it comes close, and lets go a little further.
        val (targetX, targetY) = targetCenter()
        val distance = hypot(ringX - targetX, ringY - targetY)
        val near = distance < px(if (ui.nearTarget) 96f else 72f)
        if (near != ui.nearTarget) {
            ui.nearTarget = near
            if (near) frame.performHapticFeedback(HapticFeedbackConstants.VIRTUAL_KEY)
        }
        val (x, y) = if (near) targetX to targetY else ringX to ringY
        params.x = (x - half).roundToInt()
        params.y = (y - half).roundToInt()
        apply()
    }

    private fun finish(velocityX: Float, cancelled: Boolean) {
        hideTarget()
        if (ui.nearTarget && !cancelled) {
            ui.nearTarget = false
            // Into the target, then gone.
            frame.animate().alpha(0f).scaleX(0.4f).scaleY(0.4f).setDuration(160).withEndAction { onRemove() }.start()
            return
        }
        ui.nearTarget = false
        val screen = screen()
        val safe = safe()
        // Flung toward a side, or let go close to it: it settles there; elsewhere, it stays.
        val fling = px(900f)
        val right = when {
            velocityX > fling -> true
            velocityX < -fling -> false
            else -> ringX > (safe.left + safe.right) / 2f
        }
        val fromEdge = if (right) safe.right - ringX else ringX - safe.left
        val snap = abs(velocityX) > fling || fromEdge < px(96f)
        ui.onRight = right
        anchorX = if (right) (screen.right - ringX - half).roundToInt() else (ringX - half).roundToInt()
        anchorY = (ringY - half).roundToInt()
        ui.dragging = false
        place()
        val edge = if (right) screen.right - safe.right else safe.left
        if (snap && anchorX != edge) {
            snapping = ValueAnimator.ofInt(anchorX, edge).apply {
                duration = 220
                interpolator = DecelerateInterpolator()
                addUpdateListener { anchorX = it.animatedValue as Int; place() }
                start()
            }
            anchorX = edge // where it ends, saved below
        }
        preferences.edit { putBoolean(RIGHT, right).putInt(X, if (snap) edge else anchorX).putInt(Y, anchorY) }
    }

    // -- the remove target ----------------------------------------------------

    private var target: View? = null
    private val targetSize = px(120f)
    private val dropTarget = Runnable {
        target?.let { runCatching { manager.removeView(it) } }
        target = null
    }

    private fun targetCenter(): Pair<Float, Float> = screen().width() / 2f to safe().bottom - px(24f) - targetSize / 2f

    // Its own window, added only while the badge moves: it lets no touch through.
    private fun showTarget() {
        handler.removeCallbacks(dropTarget)
        if (target == null) {
            val (x, y) = targetCenter()
            val params = WindowManager.LayoutParams(
                targetSize, targetSize, WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
                WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE or
                    WindowManager.LayoutParams.FLAG_LAYOUT_NO_LIMITS or WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN or
                    WindowManager.LayoutParams.FLAG_HARDWARE_ACCELERATED,
                PixelFormat.TRANSLUCENT,
            ).apply {
                cutout(this)
                gravity = Gravity.TOP or Gravity.START
                this.x = (x - targetSize / 2f).roundToInt()
                this.y = (y - targetSize / 2f).roundToInt()
            }
            target = compose { RemoveTarget(ui) }.also { view -> runCatching { manager.addView(view, params) } }
        }
        ui.targetShown = true
    }

    private fun hideTarget() {
        ui.targetShown = false
        handler.postDelayed(dropTarget, 220) // once it faded
    }

    // -- the screen -----------------------------------------------------------

    private fun screen(): Rect =
        if (Build.VERSION.SDK_INT >= 30) manager.currentWindowMetrics.bounds
        else context.resources.displayMetrics.let { Rect(0, 0, it.widthPixels, it.heightPixels) }

    // Without the system bars and the camera cutout.
    private fun safe(): Rect {
        val screen = screen()
        if (Build.VERSION.SDK_INT < 30) return Rect(screen.left, screen.top + px(24f), screen.right, screen.bottom - px(48f))
        val insets = manager.currentWindowMetrics.windowInsets
            .getInsetsIgnoringVisibility(WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout())
        return Rect(screen.left + insets.left, screen.top + insets.top, screen.right - insets.right, screen.bottom - insets.bottom)
    }

    // Laid out over the cutout too: its position stays the screen's.
    private fun cutout(params: WindowManager.LayoutParams) {
        if (Build.VERSION.SDK_INT >= 30) params.layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS
        else if (Build.VERSION.SDK_INT >= 28) params.layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES
    }

    private companion object {
        const val RIGHT = "badge_right"
        const val X = "badge_x"
        const val Y = "badge_y"
    }
}
