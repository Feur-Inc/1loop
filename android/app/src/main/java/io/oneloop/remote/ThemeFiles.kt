// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.content.Context
import android.net.Uri
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import java.io.File
import java.util.zip.ZipInputStream
import kotlin.math.roundToInt

// Theme files on the phone (#704, the desktop's #464 and docs/THEMES.md):
// a theme's .toml, alone or in its .zip, added from the phone's files. The
// phone draws its name, base, accents and colours, the colours it leaves out
// derived as internal/palette derives them; its shader or picture, fonts,
// blur and roundness are the desktop's. A theme is chosen as "file:<id>",
// for the app or for one project.

/** A theme read from a file: the base's tokens with its colours, and its own accents. */
class ThemeFile(val id: String, val name: String, val tokens: Tokens, val accents: List<Color>)

object ThemeFiles {
    const val PREFIX = "file:"
    private const val MAX_FILE = 64L shl 20 // a theme's zip, at most (docs/THEMES.md)
    private const val MAX_TOML = 1 shl 20

    /** The themes added on this phone, by name; read again after a change. */
    var themes by mutableStateOf<List<ThemeFile>>(emptyList())
        private set
    private var loaded = false

    private fun dir(context: Context) = File(context.filesDir, "themes")

    fun load(context: Context) {
        if (loaded) return
        loaded = true
        reload(context)
    }

    private fun reload(context: Context) {
        themes = dir(context).listFiles { file -> file.name.endsWith(".toml") }.orEmpty()
            .mapNotNull { file -> runCatching { parse(file.nameWithoutExtension, file.readText()) }.getOrNull() }
            .sortedBy { it.name.lowercase() }
    }

    fun find(id: String) = themes.firstOrNull { it.id == id }

    /**
     * Adds the theme a file holds: a .toml, or a .zip with theme.toml (or its
     * only .toml) at its root. Its id is the file's name; one of that name is
     * replaced. Throws with the reason when it can't be read.
     */
    fun add(context: Context, uri: Uri, fileName: String): ThemeFile {
        val base = fileName.substringBeforeLast('.').lowercase().replace(Regex("[^a-z0-9._-]+"), "-").trim('-', '.')
        require(base.isNotEmpty()) { "This file has no name to give the theme." }
        val text = context.contentResolver.openInputStream(uri)?.use { input ->
            val bytes = readAtMost(input, MAX_FILE.toInt() + 1)
            require(bytes.size <= MAX_FILE) { "A theme may hold 64 MB at most." }
            if (fileName.lowercase().endsWith(".zip")) themeFromZip(bytes) else bytes.decodeToString()
        } ?: error("The file could not be read.")
        val theme = parse(base, text) // its errors, before anything is saved
        val folder = dir(context).apply { mkdirs() }
        File(folder, "$base.toml").writeText(text)
        reload(context)
        return theme
    }

    fun remove(context: Context, id: String) {
        File(dir(context), "$id.toml").delete()
        reload(context)
    }

    /** The theme of a zip: theme.toml, or its only .toml, at its root; no entry with an absolute path or "..". */
    private fun themeFromZip(bytes: ByteArray): String {
        val tomls = mutableMapOf<String, String>()
        var entries = 0
        ZipInputStream(bytes.inputStream()).use { zip ->
            while (true) {
                val entry = zip.nextEntry ?: break
                require(++entries <= 200) { "A theme may have 200 entries at most." }
                val name = entry.name
                require(!name.startsWith("/") && name.split('/', '\\').none { it == ".." }) { "$name: an entry outside the theme." }
                if (!entry.isDirectory && name.endsWith(".toml") && '/' !in name) {
                    val data = readAtMost(zip, MAX_TOML + 1)
                    require(data.size <= MAX_TOML) { "$name is too large for a theme." }
                    tomls[name] = data.decodeToString()
                }
            }
        }
        return tomls["theme.toml"] ?: tomls.values.singleOrNull() ?: error("The zip has no theme.toml at its root.")
    }

    /** Up to limit bytes of input (InputStream.readNBytes needs Android 13). */
    private fun readAtMost(input: java.io.InputStream, limit: Int): ByteArray {
        val out = java.io.ByteArrayOutputStream()
        val buffer = ByteArray(64 * 1024)
        while (out.size() < limit) {
            val n = input.read(buffer, 0, minOf(buffer.size, limit - out.size()))
            if (n < 0) break
            out.write(buffer, 0, n)
        }
        return out.toByteArray()
    }

    /** Reads a theme file's text, as palette.ParseTheme for what the phone draws. */
    fun parse(id: String, text: String): ThemeFile {
        val toml = Toml.parse(text)
        val top = toml[""].orEmpty()
        val base = when ((top["base"] as? String)?.trim()?.lowercase()) {
            null, "", "dark" -> PaletteTokens.dark
            "light" -> PaletteTokens.light
            else -> error("base: \"${top["base"]}\" is neither dark nor light")
        }
        val colors = tokenColors(base)
        val set = mutableSetOf<String>()
        for ((key, value) in toml["colors"].orEmpty()) {
            require(key in colors) { "colors.$key: not a colour 1loop knows (see docs/THEMES.md)" }
            colors[key] = parseHexAlpha(value as? String) ?: error("colors.$key: \"$value\" is not #rrggbb or #rrggbbaa")
            set += key
        }
        derive(colors, tokenColors(base), set)
        val accents = (top["accents"] as? List<*>).orEmpty().mapIndexed { i, value ->
            parseHex(value as? String) ?: error("accents[$i]: \"$value\" is not #rrggbb")
        }
        val name = (top["name"] as? String)?.trim().orEmpty().ifEmpty { id }
        return ThemeFile(id, name, tokens(colors, base.dark), accents)
    }

    // The tokens a theme's [colors] can set, by their key: the field's name in snake case, as palette's tokenField.
    private val keys = listOf(
        "background", "sidebar", "surface", "popover", "raised", "field", "code", "border", "border_soft", "border_hot", "border_hover",
        "pop_border", "text", "text_soft", "muted", "placeholder", "faint", "track", "knob_off", "danger", "close", "close_hot", "added",
        "diff_added", "diff_removed", "code_ink", "overlay", "hover", "selected", "menu_hover", "pressed", "scroll_bar", "code_chip",
        "code_edge", "glass_edge",
    )

    private fun tokenColors(t: Tokens) = keys.zip(
        listOf(
            t.background, t.sidebar, t.surface, t.popover, t.raised, t.field, t.code, t.border, t.borderSoft, t.borderHot, t.borderHover,
            t.popBorder, t.text, t.textSoft, t.muted, t.placeholder, t.faint, t.track, t.knobOff, t.danger, t.close, t.closeHot, t.added,
            t.diffAdded, t.diffRemoved, t.codeInk, t.overlay, t.hover, t.selected, t.menuHover, t.pressed, t.scrollBar, t.codeChip,
            t.codeEdge, t.glassEdge,
        ),
    ).toMap(LinkedHashMap())

    private fun tokens(c: Map<String, Color>, dark: Boolean) = Tokens(
        background = c.getValue("background"), sidebar = c.getValue("sidebar"), surface = c.getValue("surface"), popover = c.getValue("popover"),
        raised = c.getValue("raised"), field = c.getValue("field"), code = c.getValue("code"), border = c.getValue("border"),
        borderSoft = c.getValue("border_soft"), borderHot = c.getValue("border_hot"), borderHover = c.getValue("border_hover"),
        popBorder = c.getValue("pop_border"), text = c.getValue("text"), textSoft = c.getValue("text_soft"), muted = c.getValue("muted"),
        placeholder = c.getValue("placeholder"), faint = c.getValue("faint"), track = c.getValue("track"), knobOff = c.getValue("knob_off"),
        danger = c.getValue("danger"), close = c.getValue("close"), closeHot = c.getValue("close_hot"), added = c.getValue("added"),
        diffAdded = c.getValue("diff_added"), diffRemoved = c.getValue("diff_removed"), codeInk = c.getValue("code_ink"),
        overlay = c.getValue("overlay"), hover = c.getValue("hover"), selected = c.getValue("selected"), menuHover = c.getValue("menu_hover"),
        pressed = c.getValue("pressed"), scrollBar = c.getValue("scroll_bar"), codeChip = c.getValue("code_chip"), codeEdge = c.getValue("code_edge"),
        glassEdge = c.getValue("glass_edge"), dark = dark,
    )

    /**
     * Fills the colours a file leaves out from those it sets, as palette's
     * derive: the surfaces and borders from its background, the quieter
     * texts from its text, each as far from them as the base's are.
     */
    private fun derive(tokens: MutableMap<String, Color>, base: Map<String, Color>, set: Set<String>) {
        fun follow(anchor: String, keys: List<String>, toward: Color, baseToward: Color) {
            if (anchor !in set) return
            for (key in keys) if (key !in set) tokens[key] = mixLike(tokens.getValue(anchor), toward, base.getValue(anchor), baseToward, base.getValue(key))
        }
        follow("background", listOf("sidebar", "surface", "popover", "raised", "field", "code", "border", "border_soft",
            "border_hot", "border_hover", "pop_border", "track", "diff_added", "diff_removed"), tokens.getValue("text"), base.getValue("text"))
        follow("text", listOf("text_soft", "muted", "placeholder", "faint", "knob_off", "code_ink"), tokens.getValue("background"), base.getValue("background"))
    }

    private fun mixLike(from: Color, to: Color, baseFrom: Color, baseTo: Color, want: Color): Color {
        fun byte(v: Float) = (v * 255f).roundToInt()
        fun ch(f: Float, t: Float, bf: Float, bt: Float, w: Float): Int {
            val span = (byte(bt) - byte(bf)).toDouble()
            val k = if (kotlin.math.abs(span) > 1) (byte(w) - byte(bf)) / span else 0.0
            return (byte(f) + (byte(t) - byte(f)) * k + 0.5).coerceIn(0.0, 255.0).toInt()
        }
        return Color(
            red = ch(from.red, to.red, baseFrom.red, baseTo.red, want.red),
            green = ch(from.green, to.green, baseFrom.green, baseTo.green, want.green),
            blue = ch(from.blue, to.blue, baseFrom.blue, baseTo.blue, want.blue),
            alpha = byte(want.alpha),
        )
    }

    private fun parseHexAlpha(text: String?): Color? {
        val hex = text?.trim()?.removePrefix("#") ?: return null
        if (hex.length != 8) return parseHex(hex)
        val alpha = hex.substring(6).toIntOrNull(16) ?: return null
        return parseHex(hex.substring(0, 6))?.copy(alpha = alpha / 255f)
    }
}

/**
 * The TOML a theme file uses: key = "string" | number | true/false | an
 * array of strings on one line, in tables ([colors], [blur]...); comments
 * after #. Values by table ("" for the top), by key.
 */
internal object Toml {
    fun parse(text: String): Map<String, Map<String, Any>> {
        val tables = linkedMapOf<String, MutableMap<String, Any>>("" to linkedMapOf())
        var table = ""
        text.lines().forEachIndexed { index, raw ->
            val line = stripComment(raw).trim()
            if (line.isEmpty()) return@forEachIndexed
            fun fail(message: String): Nothing = error("line ${index + 1}: $message")
            if (line.startsWith("[")) {
                if (!line.endsWith("]") || line.startsWith("[[")) fail("a table is [name]")
                table = line.removeSurrounding("[", "]").trim()
                tables.getOrPut(table) { linkedMapOf() }
                return@forEachIndexed
            }
            val at = line.indexOf('=')
            if (at <= 0) fail("expected key = value")
            val key = line.substring(0, at).trim().removeSurrounding("\"")
            tables.getValue(table)[key] = value(line.substring(at + 1).trim()) ?: fail("$key: a value 1loop can't read")
        }
        return tables
    }

    private fun value(text: String): Any? = when {
        text.startsWith("\"") && text.endsWith("\"") && text.length >= 2 -> unquote(text)
        text.startsWith("[") && text.endsWith("]") -> text.removeSurrounding("[", "]").split(',').map { it.trim() }.filter { it.isNotEmpty() }.map { unquote(it) ?: return null }
        text == "true" || text == "false" -> text == "true"
        else -> text.toDoubleOrNull()
    }

    private fun unquote(text: String): String? =
        if (text.length >= 2 && text.startsWith("\"") && text.endsWith("\"")) text.substring(1, text.length - 1).replace("\\\"", "\"").replace("\\\\", "\\") else null

    /** The line without its comment: a # outside a string. */
    private fun stripComment(line: String): String {
        var quoted = false
        for ((i, c) in line.withIndex()) {
            if (c == '"' && (i == 0 || line[i - 1] != '\\')) quoted = !quoted
            if (c == '#' && !quoted) return line.substring(0, i)
        }
        return line
    }
}
