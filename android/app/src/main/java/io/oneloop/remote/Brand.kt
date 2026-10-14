package io.oneloop.remote

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathMeasure
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import kotlin.math.floor
import kotlin.math.min

private fun svg(d: String): Path = PathParser().parsePathString(d).toPath()

// CSS timing functions of the DA pages.
val CssEase = CubicBezierEasing(0.25f, 0.1f, 0.25f, 1f)
val CssEaseInOut = CubicBezierEasing(0.42f, 0f, 0.58f, 1f)
val CssPop = CubicBezierEasing(0.34f, 1.56f, 0.64f, 1f)
val CssDraw = CubicBezierEasing(0.65f, 0f, 0.35f, 1f)
private val CssEaseOut = CubicBezierEasing(0f, 0f, 0.58f, 1f)

// ---------------------------------------------------------------------------
// Launch: DA/1loop-lancement.html. "1" drops in, "l" rises, the loop is drawn
// left to right, "p" slides in while the whole word settles from 96 %; the
// layer then fades to the app, like the desktop launch. A tap skips it.

private object Wordmark {
    // viewBox -16 65.6 293.41 132.8
    val one = svg("M28.896 160.0V92.128H26.88L16.352 116.992H1.344L17.36 81.6H43.232V160.0Z")
    val letterL = svg("M52.864 160.0V81.6H66.976V160.0Z")
    // The <rect rx> as a path starting where the browser starts it.
    val loop = svg("M103.71 109.94 H166.2 A22.29 22.29 0 0 1 166.2 154.52 H103.71 A22.29 22.29 0 0 1 103.71 109.94 Z")
    val letterP = svg("M202.944 182.4V104.448H216.832V111.168H218.848Q220.752 107.92 224.784 105.4Q228.816 102.88 236.32 102.88Q243.04 102.88 248.752 106.184Q254.464 109.488 257.936 115.872Q261.408 122.256 261.408 131.328V133.12Q261.408 142.192 257.936 148.576Q254.464 154.96 248.752 158.264Q243.04 161.568 236.32 161.568Q231.28 161.568 227.864 160.392Q224.448 159.216 222.376 157.368Q220.304 155.52 219.072 153.616H217.056V182.4ZM232.064 149.248Q238.672 149.248 242.984 145.048Q247.296 140.848 247.296 132.784V131.664Q247.296 123.6 242.928 119.4Q238.56 115.2 232.064 115.2Q225.568 115.2 221.2 119.4Q216.832 123.6 216.832 131.664V132.784Q216.832 140.848 221.2 145.048Q225.568 149.248 232.064 149.248Z")
    const val left = -16f
    const val top = 65.6f
    const val width = 293.41f
    const val height = 132.8f
}

private fun progress(t: Float, delay: Float, length: Float) = ((t - delay) / length).coerceIn(0f, 1f)

@Composable
fun LaunchScreen(onDone: () -> Unit) {
    var elapsed by remember { mutableLongStateOf(0L) }
    LaunchedEffect(Unit) {
        val start = withFrameNanos { it }
        while (true) {
            elapsed = (withFrameNanos { it } - start) / 1_000_000
            if (elapsed >= 2200) break
        }
        onDone()
    }
    val elapsedMilliseconds = elapsed.toFloat()
    val fade = CssEaseOut.transform(progress(elapsedMilliseconds, 1800f, 400f))
    Box(
        Modifier.fillMaxSize().background(Palette.background.copy(alpha = 1f - fade))
            .clickable(remember { MutableInteractionSource() }, indication = null, onClick = onDone),
        contentAlignment = Alignment.Center,
    ) {
        Canvas(Modifier.fillMaxSize()) {
            val wordmarkScale = min(size.width * 0.8f, 640.dp.toPx()) / Wordmark.width
            val settle = 0.96f + 0.04f * CssEaseOut.transform(progress(elapsedMilliseconds, 0f, 1500f))
            val alpha = 1f - fade
            translate(size.width / 2, size.height / 2) {
                scale(wordmarkScale * settle, wordmarkScale * settle, pivot = Offset.Zero) {
                    translate(-(Wordmark.left + Wordmark.width / 2), -(Wordmark.top + Wordmark.height / 2)) {
                        val one = progress(elapsedMilliseconds, 0f, 520f)
                        translate(0f, -40f * (1 - CssPop.transform(one))) {
                            drawPath(Wordmark.one, Palette.amber, alpha = one * alpha)
                        }
                        val letterLProgress = progress(elapsedMilliseconds, 120f, 460f)
                        translate(0f, 28f * (1 - CssPop.transform(letterLProgress))) {
                            drawPath(Wordmark.letterL, Palette.text, alpha = letterLProgress * alpha)
                        }
                        drawPartial(Wordmark.loop, CssDraw.transform(progress(elapsedMilliseconds, 280f, 700f)), Palette.text.copy(alpha = alpha), 14.11f)
                        val letterPProgress = progress(elapsedMilliseconds, 760f, 480f)
                        translate(28f * (1 - CssPop.transform(letterPProgress)), 0f) {
                            drawPath(Wordmark.letterP, Palette.text, alpha = letterPProgress * alpha)
                        }
                    }
                }
            }
        }
    }
}

// drawPartial strokes the first fraction of a path, like a CSS dash offset.
private fun DrawScope.drawPartial(path: Path, fraction: Float, color: Color, width: Float, cap: StrokeCap = StrokeCap.Butt) {
    if (fraction <= 0f) return
    val measure = PathMeasure().apply { setPath(path, false) }
    val part = Path()
    measure.getSegment(0f, measure.length * fraction, part, true)
    drawPath(part, color, style = Stroke(width = width, cap = cap, join = StrokeJoin.Round))
}

// ---------------------------------------------------------------------------
// The ring mark (DA/1loop-anneau.svg), and its intro (1loop-anneau-intro.html).

private val ringOpen = svg("M95.24 30.43 A46 46 0 1 1 67.99 14.70")
private val markOne = svg("M49 46 L62 38 L62 80")

@Composable
fun RingMark(size: Dp, modifier: Modifier = Modifier, intro: Boolean = false) {
    val drawn = remember { Animatable(if (intro) 0f else 1f) }
    LaunchedEffect(intro) { if (intro) drawn.animateTo(1f, tween(1240, easing = { it })) }
    Canvas(modifier.size(size)) {
        val elapsedMilliseconds = drawn.value * 1240f
        scale(size.toPx() / 120f, size.toPx() / 120f, pivot = Offset.Zero) {
            val ring = CssDraw.transform(progress(elapsedMilliseconds, 0f, 900f))
            rotate(-120f * (1 - ring), pivot = Offset(60f, 60f)) {
                drawPartial(ringOpen, ring, Palette.amber, 12f, StrokeCap.Round)
            }
            val dot = CssPop.transform(progress(elapsedMilliseconds, 820f, 420f))
            drawCircle(Palette.text, radius = 6f * dot, center = Offset(83f, 20.2f))
            drawPartial(markOne, CssDraw.transform(progress(elapsedMilliseconds, 480f, 520f)), Palette.text, 12f, StrokeCap.Round)
        }
    }
}

// ---------------------------------------------------------------------------
// Work ring: DA/1loop-anneau-travail.html, with the desktop's added states.
// Same layers and transitions: the open ring turns once per 1.1 s across
// states, icons fade (220 ms ease) and pop from 70 % (320 ms), done and
// stopped close the ring (250 ms), the thinking dots breathe over 1.2 s.

private class Glyph(val paths: List<Pair<Path, Float>>, val dots: List<Pair<Offset, Float>> = emptyList(), val color: Color = Palette.text)

private fun glyph(vararg paths: Pair<String, Float>, dots: List<Pair<Offset, Float>> = emptyList(), color: Color = Palette.text) =
    Glyph(paths.map { svg(it.first) to it.second }, dots, color)

// The paths of branding.go (glyphMarkup), stroke widths included.
private val glyphs = mapOf(
    "write" to glyph("M49 40 C55.5 40 60 42.5 60 48 C60 42.5 64.5 40 71 40 M60 48 V72 M49 80 C55.5 80 60 77.5 60 72 C60 77.5 64.5 80 71 80" to 8f),
    "wait" to glyph("M60 60 V33 M60 60 L77 71" to 8.5f, dots = listOf(Offset(60f, 60f) to 6f)),
    "call" to glyph("M52 41 C46 41 47 49 47 53 C47 57 45 60 42 60 C45 60 47 63 47 67 C47 71 46 79 52 79 M68 41 C74 41 73 49 73 53 C73 57 75 60 78 60 C75 60 73 63 73 67 C73 71 74 79 68 79" to 7f),
    "read" to glyph("M44 49 H76 M44 60 H76 M44 71 H64" to 8f),
    "list" to glyph("M40.4 50.2 C40.4 47.82 42.22 46 44.6 46 H52.3 L57.2 50.9 H75.4 C77.78 50.9 79.6 52.72 79.6 55.1 V71.2 C79.6 73.58 77.78 75.4 75.4 75.4 H44.6 C42.22 75.4 40.4 73.58 40.4 71.2 Z" to 8f),
    "edit" to glyph("M46 75 L48.5 64.5 L67 46 L75 54 L56.5 72.5 Z M63 50 L71 58" to 7f),
    "bash" to glyph("M44 48 L55 59 L44 70 M60 72 H76" to 8f),
    "approval" to glyph("M60 39.7 L75.95 46.23 V58.55 C75.95 68.41 69.57 75.37 60 79.58 C50.43 75.37 44.05 68.41 44.05 58.55 V46.23 Z" to 8f),
    "compact" to glyph("M60 37 V52 M52.5 45 L60 52 L67.5 45 M60 83 V68 M52.5 75 L60 68 L67.5 75 M42 60 H78" to 8f),
    "done" to glyph("M45 61 L55 71 L76 50" to 9f, color = Palette.amber),
    "stopped" to glyph("M47 47 L73 73 M73 47 L47 73" to 9f),
)

private val workStates = glyphs.keys + "think"

/** One centre icon fading in or out; times in ms since the ring started. */
private class GlyphLayer(val state: String, val at: Float, var leaving: Float = -1f)

private fun thinkOpacity(t: Float): Float {
    val x = t - floor(t)
    return if (x < 0.4f) 0.25f + 0.75f * CssEaseInOut.transform(x / 0.4f)
    else 1f - 0.75f * CssEaseInOut.transform((x - 0.4f) / 0.6f)
}

@Composable
fun WorkRing(state: String, size: Dp, modifier: Modifier = Modifier) {
    val shown = if (state in workStates) state else "think"
    val closed = shown == "done" || shown == "stopped"
    var now by remember { mutableLongStateOf(0L) } // ms, drives every layer
    var angle by remember { mutableStateOf(0f) }
    val layers = remember { mutableStateListOf<GlyphLayer>() }
    LaunchedEffect(Unit) {
        val start = withFrameNanos { it }
        var last = start
        while (true) {
            withFrameNanos { frame ->
                if (!closedRef(layers)) angle = (angle + (frame - last) / 1e6f / 1100f * 360f) % 360f
                last = frame
                now = (frame - start) / 1_000_000
            }
        }
    }
    LaunchedEffect(shown) {
        layers.forEach { if (it.leaving < 0) it.leaving = now.toFloat() }
        layers.add(GlyphLayer(shown, now.toFloat()))
    }
    val open by animateFloatAsState(if (closed) 0f else 1f, tween(250, easing = CssEase), label = "open")
    val full by animateFloatAsState(if (closed) 1f else 0f, tween(250, easing = CssEase), label = "full")
    Canvas(modifier.size(size)) {
        val elapsedMilliseconds = now.toFloat()
        layers.removeAll { it.leaving >= 0 && elapsedMilliseconds - it.leaving > 320f }
        scale(this.size.width / 120f, this.size.width / 120f, pivot = Offset.Zero) {
            rotate(angle, pivot = Offset(60f, 60f)) {
                drawPath(ringOpen, Palette.amber, alpha = open, style = Stroke(12f, cap = StrokeCap.Round))
                if (!closed) drawCircle(Palette.text, 6f, Offset(83f, 20.2f)) // .dot has no transition
            }
            val fullColor = if (shown == "stopped") Color(0xFF5C5C5C) else Palette.amber
            drawCircle(fullColor, 46f, Offset(60f, 60f), alpha = full, style = Stroke(12f))
            for (layer in layers) {
                val since = if (layer.leaving >= 0) elapsedMilliseconds - layer.leaving else elapsedMilliseconds - layer.at
                var opacity = CssEase.transform((since / 220f).coerceIn(0f, 1f))
                var scale = 0.7f + 0.3f * CssPop.transform((since / 320f).coerceIn(0f, 1f))
                if (layer.leaving >= 0) {
                    opacity = 1 - opacity
                    scale = 1.7f - scale
                }
                scale(scale, scale, pivot = Offset(60f, 60f)) {
                    if (layer.state == "think") {
                        listOf(46f, 60f, 74f).forEachIndexed { i, x ->
                            drawCircle(Palette.text, 5f, Offset(x, 60f), alpha = opacity * thinkOpacity(elapsedMilliseconds / 1200f - 0.125f * i))
                        }
                    } else glyphs[layer.state]?.let { glyph ->
                        for ((path, width) in glyph.paths) {
                            drawPath(path, glyph.color, alpha = opacity, style = Stroke(width, cap = StrokeCap.Round, join = StrokeJoin.Round))
                        }
                        for ((center, radius) in glyph.dots) drawCircle(glyph.color, radius, center, alpha = opacity)
                    }
                }
            }
        }
    }
}

// The spin pauses while the newest layer is a closed state, like the page.
private fun closedRef(layers: List<GlyphLayer>) = layers.lastOrNull()?.state.let { it == "done" || it == "stopped" }
