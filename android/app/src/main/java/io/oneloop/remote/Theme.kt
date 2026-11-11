// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.app.Activity
import android.content.Context
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.core.content.edit
import androidx.core.view.WindowCompat
import kotlin.math.pow
import kotlin.math.roundToInt

// The desktop palette, token for token (PaletteTokens.kt is generated from
// internal/palette). Reads are Compose state: a change of theme or accent
// redraws whatever used the colours.
object Palette {
    private var tokens by mutableStateOf(PaletteTokens.dark)
    private var accented by mutableStateOf(derive(PaletteTokens.dark, accentColor(PaletteTokens.defaultAccent)))

    val dark get() = tokens.dark
    val background get() = tokens.background
    val sidebar get() = tokens.sidebar
    val surface get() = tokens.surface
    val popover get() = tokens.popover
    val raised get() = tokens.raised
    val field get() = tokens.field
    val code get() = tokens.code
    val border get() = tokens.border
    val borderSoft get() = tokens.borderSoft
    val borderHot get() = tokens.borderHot
    val borderHover get() = tokens.borderHover
    val popBorder get() = tokens.popBorder
    val text get() = tokens.text
    val textSoft get() = tokens.textSoft
    val muted get() = tokens.muted
    val placeholder get() = tokens.placeholder
    val faint get() = tokens.faint
    val track get() = tokens.track
    val danger get() = tokens.danger
    val added get() = tokens.added
    val diffAdded get() = tokens.diffAdded
    val diffRemoved get() = tokens.diffRemoved
    val codeInk get() = tokens.codeInk
    /** Light over dark surfaces, shade over light ones: hovers, sheens, edges. */
    val overlay get() = tokens.overlay
    val hover get() = tokens.hover
    val selected get() = tokens.selected
    val glassEdge get() = tokens.glassEdge

    /** The accent, fitted to the theme (the desktop's colAmber). */
    val amber get() = accented.accent
    val amberHot get() = accented.accentHot
    /** Text and icons on the accent. */
    val ink get() = accented.onAccent
    val link get() = accented.link

    fun set(theme: Tokens, accent: Color) {
        if (theme !== tokens) tokens = theme
        val next = derive(theme, accent)
        if (next != accented) accented = next
    }
}

// The accent made to read on a theme, as palette.Derive does on the desktop.
data class Accented(
    val accent: Color, val accentHot: Color, val onAccent: Color, val link: Color,
    val focus: Color, val selection: Color, val ring: Color,
)

private val white = Color(0xFFFFFFFF)
private val black = Color(0xFF000000)
private val inkDark = Color(0xFF111111)

private fun channel(v: Float): Int = (v * 255f).roundToInt()

/** The desktop's Mix: each channel rounded as Go rounds it. */
fun mix(a: Color, b: Color, f: Double): Color {
    fun l(x: Int, y: Int) = (x + (y - x) * f + 0.5).toInt()
    return Color(
        red = l(channel(a.red), channel(b.red)), green = l(channel(a.green), channel(b.green)),
        blue = l(channel(a.blue), channel(b.blue)), alpha = l(channel(a.alpha), channel(b.alpha)),
    )
}

/** WCAG's relative luminance, in doubles as on the desktop. */
fun luminance(c: Color): Double {
    fun lin(v: Int): Double {
        val s = v / 255.0
        return if (s <= 0.04045) s / 12.92 else ((s + 0.055) / 1.055).pow(2.4)
    }
    return 0.2126 * lin(channel(c.red)) + 0.7152 * lin(channel(c.green)) + 0.0722 * lin(channel(c.blue))
}

fun contrast(a: Color, b: Color): Double {
    val la = luminance(a)
    val lb = luminance(b)
    return (maxOf(la, lb) + 0.05) / (minOf(la, lb) + 0.05)
}

private fun Color.withAlphaByte(a: Int) = copy(alpha = a / 255f)

fun derive(tokens: Tokens, input: Color): Accented {
    var accent = input.copy(alpha = 1f)
    val toward = if (tokens.dark) white else black
    var step = 1
    while (contrast(accent, tokens.background) < 3.2 && step <= 20) {
        accent = mix(accent, toward, 0.08)
        step++
    }
    val hot = if (tokens.dark) mix(accent, white, 0.24) else mix(accent, black, 0.12)
    return Accented(
        accent = accent, accentHot = hot,
        onAccent = if (contrast(accent, inkDark) >= contrast(accent, white)) inkDark else white,
        link = if (tokens.dark) hot else accent,
        focus = accent.withAlphaByte(0x2a), selection = accent.withAlphaByte(0x40), ring = accent.withAlphaByte(0x99),
    )
}

/** A preset's id or #rrggbb: anything else is amber. */
fun accentColor(setting: String?): Color =
    PaletteTokens.accents.firstOrNull { it.id == setting }?.color ?: parseHex(setting) ?: PaletteTokens.accents.first().color

fun parseHex(text: String?): Color? {
    val hex = text?.trim()?.removePrefix("#") ?: return null
    if (hex.length != 6) return null
    val value = hex.toLongOrNull(16) ?: return null
    return Color(0xFF000000 or value)
}

fun hex(c: Color) = "#%02x%02x%02x".format(channel(c.red), channel(c.green), channel(c.blue))

// The theme setting with the accent, kept on the phone: dark, light or
// system (Android's dark theme), and a preset or #rrggbb.
object ThemeChoice {
    private const val PREFERENCES = "1loop-theme"
    var theme by mutableStateOf("dark")
        private set
    var accent by mutableStateOf(PaletteTokens.defaultAccent)
        private set
    private var loaded = false

    fun load(context: Context) {
        if (loaded) return
        loaded = true
        val prefs = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
        theme = prefs.getString("theme", null) ?: "dark"
        accent = prefs.getString("accent", null) ?: PaletteTokens.defaultAccent
    }

    fun set(context: Context, theme: String = this.theme, accent: String = this.accent) {
        this.theme = theme
        this.accent = accent
        context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).edit { putString("theme", theme); putString("accent", accent) }
    }

    /** The accent as the settings list names it: a preset, or custom. */
    val accentSetting get() = if (PaletteTokens.accents.any { it.id == accent }) accent else "custom"

    fun tokens(systemDark: Boolean) = when (theme) {
        "light" -> PaletteTokens.light
        "system" -> if (systemDark) PaletteTokens.dark else PaletteTokens.light
        else -> PaletteTokens.dark
    }
}

val Inter = FontFamily(
    Font(R.font.inter_regular, FontWeight.Normal),
    Font(R.font.inter_medium, FontWeight.Medium),
    Font(R.font.inter_semibold, FontWeight.SemiBold),
)

object Type {
    val body get() = TextStyle(fontFamily = Inter, fontSize = 15.sp, lineHeight = 22.sp, color = Palette.text)
    val small get() = TextStyle(fontFamily = Inter, fontSize = 13.sp, lineHeight = 18.sp, color = Palette.muted)
    val caption get() = TextStyle(fontFamily = Inter, fontSize = 12.sp, lineHeight = 16.sp, color = Palette.faint)
    val title get() = TextStyle(fontFamily = Inter, fontSize = 17.sp, lineHeight = 22.sp, fontWeight = FontWeight.SemiBold, color = Palette.text)
    val headline get() = TextStyle(fontFamily = Inter, fontSize = 24.sp, lineHeight = 30.sp, color = Palette.text)
    val mono get() = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 13.sp, lineHeight = 19.sp, color = Palette.text)
}

@Composable
fun OneLoopTheme(content: @Composable () -> Unit) {
    val context = LocalContext.current
    ThemeChoice.load(context)
    // isSystemInDarkTheme follows the configuration: Android's dark theme,
    // live, as it changes.
    val tokens = ThemeChoice.tokens(isSystemInDarkTheme())
    Palette.set(tokens, accentColor(ThemeChoice.accent))
    val view = LocalView.current
    val activity = context as? Activity
    if (activity != null && !view.isInEditMode) {
        SideEffect {
            // Dark status and navigation bar icons over the light theme.
            WindowCompat.getInsetsController(activity.window, view).run {
                isAppearanceLightStatusBars = !tokens.dark
                isAppearanceLightNavigationBars = !tokens.dark
            }
        }
    }
    val scheme = if (tokens.dark) {
        darkColorScheme(
            primary = Palette.amber, onPrimary = Palette.ink, background = Palette.background,
            onBackground = Palette.text, surface = Palette.surface, onSurface = Palette.text,
            surfaceContainerLow = Palette.surface, surfaceContainer = Palette.popover,
            surfaceContainerHigh = Palette.popover, onSurfaceVariant = Palette.muted,
            outline = Palette.border, error = Palette.danger,
        )
    } else {
        lightColorScheme(
            primary = Palette.amber, onPrimary = Palette.ink, background = Palette.background,
            onBackground = Palette.text, surface = Palette.surface, onSurface = Palette.text,
            surfaceContainerLow = Palette.surface, surfaceContainer = Palette.popover,
            surfaceContainerHigh = Palette.popover, surfaceContainerHighest = Palette.raised, onSurfaceVariant = Palette.muted,
            outline = Palette.border, error = Palette.danger, surfaceTint = Palette.surface,
        )
    }
    MaterialTheme(colorScheme = scheme, content = content)
}
