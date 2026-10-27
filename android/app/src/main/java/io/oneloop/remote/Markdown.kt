package io.oneloop.remote

import android.content.Context
import android.content.res.Resources
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.RectF
import android.text.Spannable
import android.text.SpannableStringBuilder
import android.text.Spanned
import android.text.TextPaint
import android.text.style.ClickableSpan
import android.text.style.ReplacementSpan
import android.util.TypedValue
import android.view.View
import android.widget.TextView
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.res.ResourcesCompat
import androidx.core.widget.TextViewCompat
import io.noties.markwon.AbstractMarkwonPlugin
import io.noties.markwon.Markwon
import io.noties.markwon.core.MarkwonTheme
import io.noties.markwon.core.spans.LinkSpan
import io.noties.markwon.ext.latex.JLatexMathPlugin
import io.noties.markwon.ext.strikethrough.StrikethroughPlugin
import io.noties.markwon.ext.tables.TableAwareMovementMethod
import io.noties.markwon.ext.tables.TablePlugin
import io.noties.markwon.ext.tables.TableTheme
import io.noties.markwon.ext.tasklist.TaskListPlugin
import io.noties.markwon.image.AsyncDrawableScheduler
import io.noties.markwon.inlineparser.MarkwonInlineParserPlugin
import org.commonmark.node.Document
import org.commonmark.ext.gfm.tables.TableBlock
import org.commonmark.node.FencedCodeBlock
import org.commonmark.node.IndentedCodeBlock
import java.util.concurrent.Executors
import kotlin.math.roundToInt

private sealed interface MarkdownPart {
    data class Text(val content: Spanned, val columns: Int = 0) : MarkdownPart
    data class Code(val language: String, val content: String) : MarkdownPart
}

private val mathExecutor = Executors.newFixedThreadPool(2)

private fun markdownRenderer(context: Context, textSize: Float) = Markwon.builder(context)
    .usePlugin(MarkwonInlineParserPlugin.create())
    .usePlugin(TablePlugin.create(TableTheme.emptyBuilder()
        .tableBorderColor(Palette.border.toArgb())
        .tableBorderWidth(context.resources.displayMetrics.density.toInt().coerceAtLeast(1))
        .tableCellPadding((8 * context.resources.displayMetrics.density).toInt())
        .tableHeaderRowBackgroundColor(Palette.raised.toArgb())
        .tableOddRowBackgroundColor(Palette.code.toArgb())
        .tableEvenRowBackgroundColor(Palette.code.toArgb())
        .build()))
    .usePlugin(StrikethroughPlugin.create())
    .usePlugin(TaskListPlugin.create(context))
    .usePlugin(JLatexMathPlugin.create(textSize) { builder ->
        builder.inlinesEnabled(true).executorService(mathExecutor)
        builder.theme().textColor(Palette.text.toArgb())
    })
    .usePlugin(object : AbstractMarkwonPlugin() {
        override fun configureTheme(builder: MarkwonTheme.Builder) {
            builder.linkColor(Palette.amber.toArgb())
                .codeBackgroundColor(Palette.raised.toArgb())
                .blockQuoteColor(Palette.muted.toArgb())
                .headingBreakHeight(0)
        }
    })
    .build()

// Markwon uses $$ for inline math. Normalize the model's common TeX delimiters,
// preserving code examples and escaped dollars verbatim. Incomplete streaming
// formulas remain readable until their closing delimiter arrives. A citation
// tag (<cite file="…" lines="12-30"/>) becomes a link to its chip; one still
// arriving is left out until whole.
internal fun normalizeMath(markdown: String): String = buildString {
    var index = 0
    while (index < markdown.length) {
        if (index == 0 || markdown[index - 1] == '\n') {
            val end = markdown.indexOf('\n', index).let { if (it < 0) markdown.length else it }
            val line = markdown.substring(index, end).trimStart()
            if (line.startsWith("```") || line.startsWith("~~~")) {
                val fence = line.takeWhile { it == line[0] }
                var after = end
                while (after < markdown.length) {
                    val next = markdown.indexOf('\n', after + 1).let { if (it < 0) markdown.length else it }
                    val closing = markdown.substring(after + 1, next).trim()
                    after = next
                    if (closing.startsWith(fence) && closing.all { it == fence[0] }) break
                }
                append(markdown.substring(index, after))
                index = after
                continue
            }
        }
        if (markdown[index] == '`') {
            var codeStart = index + 1
            while (codeStart < markdown.length && markdown[codeStart] == '`') codeStart++
            val fence = markdown.substring(index, codeStart)
            val end = markdown.indexOf(fence, codeStart)
            if (end >= 0) {
                append(markdown.substring(index, end + fence.length))
                index = end + fence.length
                continue
            }
        }
        if (markdown.startsWith("<cite", index)) {
            val tag = citeTag.find(markdown, index)?.takeIf { it.range.first == index }
            if (tag == null) {
                if (markdown.indexOf('>', index) < 0) break
            } else {
                val (file, from, to) = tag.destructured
                val start = from.toIntOrNull() ?: 0
                val end = to.toIntOrNull() ?: start
                val lines = when { end > start -> " · $start–$end"; start > 0 -> " · $start"; else -> "" }
                val label = (file.substringAfterLast('/') + lines).replace(Regex("""([\\`*_\[\]<>])"""), "\\\\$1")
                append("[").append(label).append("](<").append(CITE_SCHEME).append("$start-$end:$file>)")
                index = tag.range.last + 1
                continue
            }
        }
        val opening = when {
            markdown.startsWith("\\(", index) -> "\\("
            markdown.startsWith("\\[", index) -> "\\["
            markdown.startsWith("$$", index) -> "$$"
            markdown[index] == '$' -> "$"
            else -> null
        }
        if (opening != null) {
            val closing = when (opening) { "\\(" -> "\\)"; "\\[" -> "\\]"; else -> opening }
            val start = index + opening.length
            val end = markdown.indexOf(closing, start)
            val formula = if (end >= 0) markdown.substring(start, end) else ""
            // As in Pandoc, a closing $ followed by a digit is a price ("$5-$10"), not math.
            if (formula.isNotBlank() && (opening != "$" || (!formula.first().isWhitespace() && !formula.last().isWhitespace() && '\n' !in formula && markdown.getOrNull(end + 1)?.isDigit() != true))) {
                if (opening == "\\[" || opening == "$$") append("\n\n$$\n").append(formula.trim()).append("\n$$\n\n")
                else append("$$").append(formula).append("$$")
                index = end + closing.length
                continue
            }
        }
        if (markdown[index] == '\\' && index + 1 < markdown.length) {
            append(markdown[index++])
        }
        append(markdown[index++])
    }
}

private const val CITE_SCHEME = "1loop-cite:"
private val citeTag = Regex("""<cite\s+file="([^"]+)"(?:\s+lines="(\d+)(?:\s*[-–]\s*(\d+))?")?\s*/?>(?:</cite>)?""")

/** A citation's chip, drawn whole (it does not wrap): smaller muted text on a rounded tint. */
private class CiteChip : ReplacementSpan() {
    private val pad = 5 * Resources.getSystem().displayMetrics.density

    private fun small(paint: Paint) = TextPaint(paint).apply { textSize = paint.textSize * 0.85f; color = Palette.muted.toArgb() }

    override fun getSize(paint: Paint, text: CharSequence, start: Int, end: Int, metrics: Paint.FontMetricsInt?): Int {
        if (metrics != null) paint.getFontMetricsInt(metrics)
        return (small(paint).measureText(text, start, end) + 2 * pad).roundToInt()
    }

    override fun draw(canvas: Canvas, text: CharSequence, start: Int, end: Int, x: Float, top: Int, y: Int, bottom: Int, paint: Paint) {
        val small = small(paint)
        val box = RectF(x, y + small.ascent() - pad / 3, x + small.measureText(text, start, end) + 2 * pad, y + small.descent() + pad / 3)
        val fill = Paint(Paint.ANTI_ALIAS_FLAG).apply { color = Palette.raised.toArgb() }
        canvas.drawRoundRect(box, pad, pad, fill)
        fill.apply { style = Paint.Style.STROKE; strokeWidth = pad / 5; color = Palette.border.toArgb() }
        canvas.drawRoundRect(box, pad, pad, fill)
        canvas.drawText(text, start, end, x + pad, y.toFloat(), small)
    }
}

/** A tap on a chip hands the citation to the TextView's tag (Markdown's onCite). */
private class CiteSpan(val link: String) : ClickableSpan() {
    override fun onClick(widget: View) {
        @Suppress("UNCHECKED_CAST")
        val onCite = (widget.tag as? ((Citation) -> Unit)) ?: return
        val lines = link.removePrefix(CITE_SCHEME).substringBefore(':')
        onCite(Citation(link.substringAfter(':').substringAfter(':'), lines.substringBefore('-').toInt(), lines.substringAfter('-').toInt()))
    }

    override fun updateDrawState(paint: TextPaint) {
        paint.isUnderlineText = false
    }
}

/** The text rendered, its citation links made chips. */
private fun chips(rendered: Spanned): Spanned {
    val links = rendered.getSpans(0, rendered.length, LinkSpan::class.java).filter { it.link.startsWith(CITE_SCHEME) }
    if (links.isEmpty()) return rendered
    val text = rendered as? Spannable ?: SpannableStringBuilder(rendered)
    for (link in links) {
        val start = text.getSpanStart(link)
        val end = text.getSpanEnd(link)
        text.removeSpan(link)
        text.setSpan(CiteSpan(link.link), start, end, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
        text.setSpan(CiteChip(), start, end, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
    }
    return text
}

private fun markdownParts(renderer: Markwon, text: String): List<MarkdownPart> {
    val parts = mutableListOf<MarkdownPart>()
    val parsed = renderer.parse(normalizeMath(text))
    var section = Document()
    fun flush() {
        if (section.firstChild != null) parts.add(MarkdownPart.Text(chips(renderer.render(section))))
        section = Document()
    }
    var node = parsed.firstChild
    while (node != null) {
        val next = node.next
        when (node) {
            is TableBlock -> {
                flush()
                val columns = generateSequence(node.firstChild?.firstChild?.firstChild) { it.next }.count()
                val table = Document().apply { appendChild(node) }
                parts.add(MarkdownPart.Text(renderer.render(table), columns))
            }
            is FencedCodeBlock -> {
                flush()
                parts.add(MarkdownPart.Code(node.info.orEmpty().substringBefore(' '), node.literal.removeSuffix("\n")))
            }
            is IndentedCodeBlock -> {
                flush()
                parts.add(MarkdownPart.Code("", node.literal.removeSuffix("\n")))
            }
            else -> section.appendChild(node)
        }
        node = next
    }
    flush()
    return parts
}

@Composable
fun Markdown(text: String, modifier: Modifier = Modifier, onCite: ((Citation) -> Unit)? = null) {
    val context = LocalContext.current
    val density = LocalDensity.current
    val textSize = with(density) { Type.body.fontSize.toPx() }
    val renderer = remember(context, textSize) { markdownRenderer(context, textSize) }
    val parts = remember(renderer, text) { markdownParts(renderer, text) }
    Column(modifier, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        for (part in parts) when (part) {
            is MarkdownPart.Code -> CodeWell(part.content, part.language.ifEmpty { "code" })
            is MarkdownPart.Text -> if (part.columns > 0) {
                BoxWithConstraints(Modifier.fillMaxWidth()) {
                    val tableWidth = maxOf(maxWidth, (part.columns * 140).dp)
                    Row(Modifier.horizontalScroll(rememberScrollState())) {
                        MarkdownText(renderer, part.content, textSize, Modifier.width(tableWidth), onCite)
                    }
                }
            } else MarkdownText(renderer, part.content, textSize, Modifier.fillMaxWidth(), onCite)
        }
    }
}

/**
 * A TextView asks for a new layout only up to a parent that is not marked for
 * one already. Compose's AndroidView holder can stay marked (measured without
 * being laid out again): it then never heard that the text grew, the view kept
 * its old height and scrolled its text inside it (#68). Asking the holder's
 * parent too, Compose's views handler, has the holder measured again.
 */
private class MarkdownTextView(context: Context) : TextView(context) {
    override fun requestLayout() {
        super.requestLayout()
        val holder = parent as? View ?: return
        if (holder.isLayoutRequested) holder.parent?.requestLayout()
    }
}

@Composable
private fun MarkdownText(renderer: Markwon, text: Spanned, textSize: Float, modifier: Modifier, onCite: ((Citation) -> Unit)?) {
    val lineHeight = with(LocalDensity.current) { Type.body.lineHeight.toPx() }.toInt()
    AndroidView(
        modifier = modifier,
        factory = { MarkdownTextView(it).apply {
            setTextColor(Palette.text.toArgb())
            setLinkTextColor(Palette.amber.toArgb())
            typeface = ResourcesCompat.getFont(it, R.font.inter_regular)
            includeFontPadding = false
            setTextIsSelectable(true)
            movementMethod = TableAwareMovementMethod.create()
        } },
        update = { view ->
            view.tag = onCite
            view.setTextSize(TypedValue.COMPLEX_UNIT_PX, textSize)
            TextViewCompat.setLineHeight(view, lineHeight)
            renderer.setParsedMarkdown(view, text)
        },
        onRelease = { AsyncDrawableScheduler.unschedule(it) },
    )
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
