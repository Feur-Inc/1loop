package io.oneloop.remote

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

// The desktop palette (ui.go), colour for colour.
object Palette {
    val background = Color(0xFF121212)
    val sidebar = Color(0xFF0C0C0C)
    val surface = Color(0xFF1A1A1A)
    val popover = Color(0xFF212121)
    val raised = Color(0xFF242424)
    val code = Color(0xFF0E0E0E)
    val border = Color(0xFF2A2A2A)
    val borderSoft = Color(0xFF1E1E1E)
    val borderHot = Color(0xFF3C3C3C)
    val text = Color(0xFFF4F4F4)
    val muted = Color(0xFF8C8C8C)
    val faint = Color(0xFF5C5C5C)
    val amber = Color(0xFFFFB000)
    val ink = Color(0xFF111111)
    val danger = Color(0xFFE86A5F)
    val added = Color(0xFF7FC88F)
    val hover = Color(0x0CFFFFFF)
    val selected = Color(0x12FFFFFF)
}

val Inter = FontFamily(
    Font(R.font.inter_regular, FontWeight.Normal),
    Font(R.font.inter_medium, FontWeight.Medium),
    Font(R.font.inter_semibold, FontWeight.SemiBold),
)

object Type {
    val body = TextStyle(fontFamily = Inter, fontSize = 15.sp, lineHeight = 22.sp, color = Palette.text)
    val small = TextStyle(fontFamily = Inter, fontSize = 13.sp, lineHeight = 18.sp, color = Palette.muted)
    val caption = TextStyle(fontFamily = Inter, fontSize = 12.sp, lineHeight = 16.sp, color = Palette.faint)
    val title = TextStyle(fontFamily = Inter, fontSize = 17.sp, lineHeight = 22.sp, fontWeight = FontWeight.SemiBold, color = Palette.text)
    val headline = TextStyle(fontFamily = Inter, fontSize = 24.sp, lineHeight = 30.sp, color = Palette.text)
    val mono = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 13.sp, lineHeight = 19.sp, color = Palette.text)
}

@Composable
fun OneLoopTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = darkColorScheme(
            primary = Palette.amber, onPrimary = Palette.ink, background = Palette.background,
            onBackground = Palette.text, surface = Palette.surface, onSurface = Palette.text,
            surfaceContainerLow = Palette.surface, surfaceContainer = Palette.popover,
            surfaceContainerHigh = Palette.popover, onSurfaceVariant = Palette.muted,
            outline = Palette.border, error = Palette.danger,
        ),
        content = content,
    )
}
