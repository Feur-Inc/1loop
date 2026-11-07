// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.graphics.Bitmap
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.graphics.CompositingStrategy
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.unit.IntSize
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.graphics.rememberGraphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionOnScreen
import androidx.compose.ui.unit.IntOffset
import androidx.compose.foundation.BorderStroke
import androidx.compose.ui.graphics.Brush
import androidx.compose.foundation.border
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext

// Frosted glass and touch response. What opens over the app (a dialog, a
// sheet, a menu) frosts it, and the drawer frosts the conversation as it
// slides. A live blur is redone by the GPU on every frame, which a phone like
// the Pixel 3a cannot keep up with during a slide (a quarter of the frames
// late), so the screen is read once as the glass appears, shrunk and blurred
// on the CPU in a few milliseconds, and the frosted picture fades in over it:
// fading a still picture costs next to nothing. While nothing covers the app,
// nothing is kept or computed.

/** What covers the app now, and the frosted picture menus show. */
object Glass {
    /** Dialogs and sheets: the whole app frosts behind them. */
    var open by mutableIntStateOf(0)

    /** Menus: only what each covers shows through it, frosted ([glassMenu]). */
    var menus by mutableIntStateOf(0)

    /** The app frosted as a menu opened, at an eighth of its size, and where the app is on the screen. */
    var picture by mutableStateOf<ImageBitmap?>(null)
    var origin by mutableStateOf(Offset.Zero)
    var extent by mutableStateOf(IntSize.Zero) // the app's size, which the picture is shrunk from
}

/** Put beside a dialog or sheet: the app frosts behind it while it is shown. */
@Composable
fun GlassBackdrop() {
    DisposableEffect(Unit) {
        Glass.open++
        onDispose { Glass.open-- }
    }
}

/** Put beside an open menu: the app is read once, frosted, for the menu's glass. */
@Composable
fun GlassMenuOpen() {
    DisposableEffect(Unit) {
        Glass.menus++
        onDispose { Glass.menus-- }
    }
}

/**
 * A menu's surface as a pane of glass, as on the desktop: the part of the app
 * it covers, frosted, under a light [tint]. Until the frosted picture is
 * ready (a few frames) and without one, the tint is drawn opaque.
 */
@Composable
fun Modifier.glassMenu(tint: Color): Modifier {
    var at by remember { mutableStateOf(Offset.Zero) }
    val picture = Glass.picture
    val shown by animateFloatAsState(if (picture != null) 1f else 0f, tween(180), label = "menu glass")
    return onGloballyPositioned { at = it.positionOnScreen() }.drawBehind {
        if (picture != null && shown > 0f) {
            // The picture is the app shrunk to its own width: scale from the app's size.
            val k = picture.width / maxOf(1f, Glass.extent.width.toFloat())
            val x = ((at.x - Glass.origin.x) * k).toInt()
            val y = ((at.y - Glass.origin.y) * k).toInt()
            val w = maxOf(1, (size.width * k).toInt())
            val h = maxOf(1, (size.height * k).toInt())
            val sx = x.coerceIn(0, maxOf(0, picture.width - 1))
            val sy = y.coerceIn(0, maxOf(0, picture.height - 1))
            drawImage(
                picture, IntOffset(sx, sy), IntSize(minOf(w, picture.width - sx), minOf(h, picture.height - sy)),
                dstSize = IntSize(size.width.toInt(), size.height.toInt()), alpha = shown, filterQuality = FilterQuality.High,
            )
            drawRect(tint.copy(alpha = 1f - 0.5f * shown)) // light over the glass
            drawRect(Brush.verticalGradient(listOf(Color.White.copy(alpha = 0.10f * shown), Color.White.copy(alpha = 0.02f * shown)))) // its sheen
        } else drawRect(tint)
    }
}

/**
 * Content that frosts under glass: [cover] goes from 0 (clear) to 1
 * (frosted). The frosted picture is taken when cover leaves 0 and dropped
 * when it comes back. Cover and [hidden] are read as the glass is drawn, not
 * composed: a slide redraws the glass, it does not recompose the content.
 */
@Composable
fun FrostedBox(
    cover: () -> Float, modifier: Modifier = Modifier,
    read: Boolean? = null, // take the frosted picture (also for menus, with publish); by default, once covered
    publish: Boolean = false, // the app's root: share the picture and its place with the menus
    hidden: () -> Float = { 0f }, // pixels on the left that something opaque covers (the drawer): not drawn
    content: @Composable BoxScope.() -> Unit,
) {
    val small = rememberGraphicsLayer()
    var capture by remember { mutableStateOf(false) }
    var frost by remember { mutableStateOf<ImageBitmap?>(null) }
    val coverNow by rememberUpdatedState(cover)
    val hiddenNow by rememberUpdatedState(hidden)
    val covered by remember { derivedStateOf { coverNow() > 0.001f } }
    val reading = read ?: covered
    LaunchedEffect(reading) {
        if (!reading) {
            frost = null
            if (publish) Glass.picture = null
            return@LaunchedEffect
        }
        // A menu's glass is read once its popup has opened: reading it during
        // the popup's entrance made both late. Dialogs frost the app at once.
        if (publish && Glass.open == 0) delay(160)
        capture = true // the next frame also records an eighth-size copy, nothing more
        withFrameNanos {}
        withFrameNanos {}
        // Two small pictures from one read: dimmed for what covers the whole
        // app (baked in: no dimming layer redrawn on every frame of a slide),
        // bright for the menus' glass.
        val read = runCatching { small.toImageBitmap() }.getOrNull()
        frost = read?.let { runCatching { frosted(it, 90, 9) }.getOrNull() }
        // Finer and brighter: what a menu covers still shows through its glass as soft shapes.
        if (publish) Glass.picture = read?.let { runCatching { frosted(it, 135, 12) }.getOrNull() }
        capture = false
    }
    Box(if (publish) modifier.onGloballyPositioned { Glass.origin = it.positionOnScreen(); Glass.extent = it.size } else modifier) {
        Box(
            Modifier.matchParentSize().drawWithContent {
                drawContent()
                if (capture) {
                    val w = maxOf(1, (size.width / 8).toInt())
                    val h = maxOf(1, (size.height / 8).toInt())
                    small.record(IntSize(w, h)) { scale(1f / 8, Offset.Zero) { this@drawWithContent.drawContent() } }
                }
            },
            content = content,
        )
        val picture = frost
        if (covered && picture != null) Image(
            // ModulateAlpha: the alpha goes into the draw, with no offscreen buffer every frame.
            picture, null,
            Modifier.matchParentSize().graphicsLayer { alpha = coverNow(); compositingStrategy = CompositingStrategy.ModulateAlpha }
                .drawWithContent { clipRect(left = hiddenNow()) { this@drawWithContent.drawContent() } },
            contentScale = ContentScale.FillBounds, filterQuality = FilterQuality.High,
        )
    }
}

/** The root's cover: 1 while a dialog or sheet is open, eased; read it as the glass is drawn. */
@Composable
fun glassCover(): () -> Float {
    val cover = animateFloatAsState(if (Glass.open > 0) 1f else 0f, tween(220), label = "glass")
    return { cover.value }
}

/** The picture shrunk to [width] pixels, blurred, at [tenths] of its brightness: upscaled smoothly, it reads as frosted glass. */
private suspend fun frosted(picture: ImageBitmap, width: Int, tenths: Int): ImageBitmap = withContext(Dispatchers.Default) {
    val source = picture.asAndroidBitmap().let { if (it.config == Bitmap.Config.HARDWARE) it.copy(Bitmap.Config.ARGB_8888, false) else it }
    val height = maxOf(1, source.height * width / maxOf(1, source.width))
    val small = Bitmap.createScaledBitmap(source, width, height, true)
    val pixels = IntArray(width * height)
    small.getPixels(pixels, 0, width, 0, 0, width, height)
    repeat(3) { boxBlur(pixels, width, height, 2) }
    for (i in pixels.indices) { // a little more saturated, as frosted glass
        val c = pixels[i]
        var r = (c shr 16) and 0xff
        var g = (c shr 8) and 0xff
        var b = c and 0xff
        val grey = (r * 299 + g * 587 + b * 114) / 1000
        r = ((grey + (r - grey) * 14 / 10) * tenths / 10).coerceIn(0, 255)
        g = ((grey + (g - grey) * 14 / 10) * tenths / 10).coerceIn(0, 255)
        b = ((grey + (b - grey) * 14 / 10) * tenths / 10).coerceIn(0, 255)
        pixels[i] = (0xff shl 24) or (r shl 16) or (g shl 8) or b
    }
    Bitmap.createBitmap(pixels, width, height, Bitmap.Config.ARGB_8888).asImageBitmap()
}

/** A box blur over 2*radius+1 pixels, rows then columns, with running sums. */
private fun boxBlur(pixels: IntArray, width: Int, height: Int, radius: Int) {
    fun pass(n: Int, at: (Int) -> Int) {
        val line = IntArray(n) { pixels[at(it)] }
        var r = 0
        var g = 0
        var b = 0
        var count = 0
        for (k in 0 until minOf(radius, n)) {
            val c = line[k]; r += (c shr 16) and 0xff; g += (c shr 8) and 0xff; b += c and 0xff; count++
        }
        for (i in 0 until n) {
            val add = i + radius
            if (add < n) { val c = line[add]; r += (c shr 16) and 0xff; g += (c shr 8) and 0xff; b += c and 0xff; count++ }
            val drop = i - radius - 1
            if (drop >= 0) { val c = line[drop]; r -= (c shr 16) and 0xff; g -= (c shr 8) and 0xff; b -= c and 0xff; count-- }
            pixels[at(i)] = (0xff shl 24) or ((r / count) shl 16) or ((g / count) shl 8) or (b / count)
        }
    }
    for (y in 0 until height) pass(width) { y * width + it }
    for (x in 0 until width) pass(height) { it * width + x }
}

/** A control that sinks a little under the finger and springs back: pass the same source to its clickable. */
@Composable
fun Modifier.pressScale(source: MutableInteractionSource, depth: Float = 0.96f): Modifier {
    val pressed by source.collectIsPressedAsState()
    val scale by animateFloatAsState(if (pressed) depth else 1f, spring(dampingRatio = 0.55f, stiffness = 900f), label = "press")
    return graphicsLayer {
        scaleX = scale
        scaleY = scale
    }
}

/** Fades and rises into place once when [animate], as a new message does. */
@Composable
fun Modifier.appear(animate: Boolean): Modifier {
    if (!animate) return this
    val progress = remember { Animatable(0f) }
    LaunchedEffect(Unit) { progress.animateTo(1f, tween(320, easing = FastOutSlowInEasing)) }
    return graphicsLayer {
        alpha = progress.value
        translationY = (1f - progress.value) * 14.dp.toPx()
    }
}

/** The glass surfaces' edge: a hairline of light, brightest at the top, as glass catches it. */
val GlassEdge = BorderStroke(1.dp, Brush.verticalGradient(listOf(Color.White.copy(alpha = 0.26f), Color.White.copy(alpha = 0.06f))))

/** A dialog's or sheet's surface: translucent over the frosted app. */
val GlassSurface: Color get() = Palette.surface.copy(alpha = 0.84f)

/** A menu's shape: rounder, as a pane of glass. */
val GlassMenuShape = RoundedCornerShape(16.dp)

/** A dialog's light edge (its shape is Material's 28 dp). */
val GlassDialogEdge = Modifier.border(GlassEdge, RoundedCornerShape(28.dp))

/** Under a sheet: a lighter shade than Material's, so the frosted app shows. */
val GlassScrim = Color.Black.copy(alpha = 0.18f)
