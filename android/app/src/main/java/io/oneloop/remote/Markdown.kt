package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

// Answers are Markdown. The phone shows what the desktop shows (markdown.go):
// paragraphs, headings, lists, fenced code in a dark well, tables, and the
// inline styles. Parsing is cheap and remembered per text.

private sealed interface Block {
    data class Text(val text: String) : Block
    data class Heading(val level: Int, val text: String) : Block
    data class ListItem(val marker: String, val text: String, val indent: Int) : Block
    data class Code(val lang: String, val text: String) : Block
    data class Table(val rows: List<List<String>>) : Block
}

private val listMarker = Regex("""^(\s*)([-*+]|\d+[.)])\s+(.*)$""")
private val tableRule = Regex("""^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$""")

private fun parse(markdown: String): List<Block> {
    val lines = markdown.split('\n')
    val blocks = mutableListOf<Block>()
    val paragraph = mutableListOf<String>()
    fun flush() {
        if (paragraph.isNotEmpty()) blocks.add(Block.Text(paragraph.joinToString(" ").trim()))
        paragraph.clear()
    }
    var i = 0
    while (i < lines.size) {
        val line = lines[i]
        val trimmed = line.trim()
        when {
            trimmed.startsWith("```") -> {
                flush()
                val lang = trimmed.removePrefix("```").trim()
                val code = mutableListOf<String>()
                i++
                while (i < lines.size && !lines[i].trim().startsWith("```")) code.add(lines[i++])
                blocks.add(Block.Code(lang, code.joinToString("\n")))
            }
            trimmed.isEmpty() -> flush()
            trimmed.startsWith("#") -> {
                flush()
                val level = trimmed.takeWhile { it == '#' }.length
                blocks.add(Block.Heading(level, trimmed.drop(level).trim()))
            }
            '|' in line && i + 1 < lines.size && tableRule.matches(lines[i + 1]) -> {
                flush()
                val rows = mutableListOf(cells(line))
                i += 2
                while (i < lines.size && '|' in lines[i] && lines[i].isNotBlank()) rows.add(cells(lines[i++]))
                i--
                blocks.add(Block.Table(rows))
            }
            listMarker.matches(line) -> {
                flush()
                val match = listMarker.find(line)!!
                val marker = match.groupValues[2].let { if (it in listOf("-", "*", "+")) "•" else it }
                blocks.add(Block.ListItem(marker, match.groupValues[3], match.groupValues[1].length / 2))
            }
            else -> paragraph.add(trimmed)
        }
        i++
    }
    flush()
    return blocks
}

private fun cells(line: String) = line.trim().removePrefix("|").removeSuffix("|").split('|').map { it.trim() }

/** Inline Markdown: **bold**, *italic*, `code`, [links](url). */
fun inline(text: String): AnnotatedString = buildAnnotatedString {
    var i = 0
    while (i < text.length) {
        when {
            text.startsWith("**", i) && text.indexOf("**", i + 2) > i -> {
                val end = text.indexOf("**", i + 2)
                withStyle(SpanStyle(fontWeight = FontWeight.SemiBold)) { append(inline(text.substring(i + 2, end))) }
                i = end + 2
            }
            text[i] == '`' && text.indexOf('`', i + 1) > i -> {
                val end = text.indexOf('`', i + 1)
                withStyle(SpanStyle(fontFamily = FontFamily.Monospace, fontSize = 13.5.sp, background = Palette.raised)) {
                    append(text.substring(i + 1, end))
                }
                i = end + 1
            }
            (text[i] == '*' || text[i] == '_') && i + 1 < text.length && text[i + 1] != ' ' && text.indexOf(text[i], i + 1) > i + 1 -> {
                val end = text.indexOf(text[i], i + 1)
                withStyle(SpanStyle(fontStyle = FontStyle.Italic)) { append(text.substring(i + 1, end)) }
                i = end + 1
            }
            text[i] == '[' && text.indexOf("](", i) > i && text.indexOf(')', text.indexOf("](", i)) > 0 -> {
                val close = text.indexOf("](", i)
                val end = text.indexOf(')', close)
                withStyle(SpanStyle(color = Palette.amber)) { append(text.substring(i + 1, close)) }
                i = end + 1
            }
            else -> append(text[i++])
        }
    }
}

@Composable
fun Markdown(text: String, modifier: Modifier = Modifier) {
    val blocks = remember(text) { parse(text) }
    // Consecutive list items form one list, spaced tighter than paragraphs.
    val groups = remember(blocks) {
        blocks.fold(mutableListOf<List<Block>>()) { acc, b ->
            if (b is Block.ListItem && acc.lastOrNull()?.last() is Block.ListItem) acc[acc.size - 1] = acc.last() + b else acc.add(listOf(b))
            acc
        }
    }
    SelectionContainer(modifier) {
        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            for (group in groups) Column(verticalArrangement = Arrangement.spacedBy(4.dp)) { for (block in group) when (block) {
                is Block.Text -> Text(remember(block.text) { inline(block.text) }, style = Type.body)
                is Block.Heading -> Text(
                    remember(block.text) { inline(block.text) }, style = Type.body.copy(
                        fontWeight = FontWeight.SemiBold,
                        fontSize = when (block.level) { 1 -> 20.sp; 2 -> 18.sp; else -> 16.sp },
                    ),
                )
                is Block.ListItem -> Row(Modifier.padding(start = (block.indent * 16).dp)) {
                    Text(block.marker, style = Type.body.copy(color = Palette.muted), modifier = Modifier.width(22.dp))
                    Text(remember(block.text) { inline(block.text) }, style = Type.body)
                }
                is Block.Code -> CodeWell(block.text, block.lang.ifEmpty { "code" })
                is Block.Table -> Table(block.rows)
            } }
        }
    }
}

@Composable
fun CodeWell(code: String, caption: String, diff: Boolean = false, wrap: Boolean = false) {
    Column(
        Modifier.fillMaxWidth().background(Palette.code, RoundedCornerShape(10.dp))
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(10.dp)),
    ) {
        Row(Modifier.fillMaxWidth().padding(start = 12.dp, end = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(caption, style = Type.caption.copy(color = Palette.muted), modifier = Modifier.weight(1f))
            CopyChip { code }
        }
        Text(
            remember(code, diff) { if (diff) diffText(code) else AnnotatedString(code) }, style = Type.mono, softWrap = wrap,
            modifier = (if (wrap) Modifier.fillMaxWidth() else Modifier.horizontalScroll(rememberScrollState())).padding(start = 12.dp, end = 12.dp, bottom = 12.dp),
        )
    }
}

fun diffText(diff: String) = buildAnnotatedString {
    for ((i, line) in diff.split('\n').withIndex()) {
        if (i > 0) append('\n')
        val color = when {
            line.startsWith("+++") || line.startsWith("---") -> Palette.muted
            line.startsWith("+") -> Palette.added
            line.startsWith("-") -> Palette.danger
            line.startsWith("@@") -> Palette.faint
            else -> Palette.text
        }
        withStyle(SpanStyle(color = color)) { append(line) }
    }
}

@Composable
private fun Table(rows: List<List<String>>) {
    val columns = rows.maxOf { it.size }
    Column(
        Modifier.fillMaxWidth().horizontalScroll(rememberScrollState())
            .background(Palette.code, RoundedCornerShape(10.dp)).border(1.dp, Palette.border, RoundedCornerShape(10.dp)),
    ) {
        rows.forEachIndexed { r, row ->
            Row(if (r == 0) Modifier.background(Palette.raised) else Modifier) {
                for (c in 0 until columns) {
                    Text(
                        inline(row.getOrElse(c) { "" }),
                        style = Type.body.copy(fontSize = 14.sp, fontWeight = if (r == 0) FontWeight.SemiBold else FontWeight.Normal),
                        modifier = Modifier.width(140.dp).padding(horizontal = 10.dp, vertical = 6.dp),
                    )
                }
            }
        }
    }
}
