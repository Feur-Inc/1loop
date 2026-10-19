package io.oneloop.remote

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.Locale

// The files picked for the next message, above the composer's text: a
// preview for an image, its name and size, its state, and × to leave it out
// (the copy already sent to the workspace is not deleted).

@Composable
fun AttachmentTray(app: RemoteApp) {
    Row(
        Modifier.horizontalScroll(rememberScrollState()).padding(top = 8.dp, end = 6.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        for (attachment in app.attachments) AttachmentChip(attachment) { app.removeAttachment(attachment.key) }
    }
}

@Composable
private fun AttachmentChip(attachment: Attachment, onRemove: () -> Unit) {
    val failed = attachment.problem != null
    Row(
        Modifier.clip(RoundedCornerShape(10.dp)).background(Palette.raised)
            .border(1.dp, if (failed) Palette.danger.copy(alpha = 0.6f) else Palette.border, RoundedCornerShape(10.dp))
            .padding(start = 6.dp, top = 6.dp, bottom = 6.dp, end = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(36.dp).clip(RoundedCornerShape(6.dp)).background(Palette.code), contentAlignment = Alignment.Center) {
            val preview = if (attachment.image) thumbnail(attachment) else null
            if (preview != null) Image(preview, null, contentScale = ContentScale.Crop, modifier = Modifier.size(36.dp))
            else Icon(Icons.document, null, tint = Palette.muted, modifier = Modifier.size(18.dp))
            if (attachment.sending) Box(Modifier.size(36.dp).background(Palette.code.copy(alpha = 0.55f)), contentAlignment = Alignment.Center) {
                WorkRing("wait", 22.dp)
            }
        }
        Column(Modifier.widthIn(max = 150.dp).padding(start = 8.dp)) {
            Text(attachment.name, style = Type.small.copy(color = Palette.text), maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                attachment.problem ?: if (attachment.sending) "Sending…" else size(attachment.size),
                style = Type.caption.copy(color = if (failed) Palette.danger else Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
        }
        Icon(
            Icons.close, "Remove ${attachment.name}", tint = Palette.muted,
            modifier = Modifier.size(30.dp).clip(RoundedCornerShape(50)).clickable(onClick = onRemove).padding(8.dp),
        )
    }
}

/** A small preview, decoded at the chip's size, never the whole picture. */
@Composable
private fun thumbnail(attachment: Attachment): ImageBitmap? {
    val context = LocalContext.current
    val pixels = with(androidx.compose.ui.platform.LocalDensity.current) { 36.dp.roundToPx() }
    var image by remember(attachment.key) { mutableStateOf<ImageBitmap?>(null) }
    LaunchedEffect(attachment.key) {
        image = withContext(Dispatchers.IO) {
            runCatching {
                val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
                context.contentResolver.openInputStream(attachment.uri)?.use { BitmapFactory.decodeStream(it, null, bounds) }
                var sample = 1
                while (bounds.outWidth / (sample * 2) >= pixels && bounds.outHeight / (sample * 2) >= pixels) sample *= 2
                context.contentResolver.openInputStream(attachment.uri)?.use {
                    BitmapFactory.decodeStream(it, null, BitmapFactory.Options().apply { inSampleSize = sample })
                }?.asImageBitmap()
            }.getOrNull()
        }
    }
    return image
}

private fun size(bytes: Long) = when {
    bytes < 0 -> ""
    bytes < 1024 -> "$bytes B"
    bytes < 1024 * 1024 -> String.format(Locale.ROOT, "%.0f KB", bytes / 1024.0)
    else -> String.format(Locale.ROOT, "%.1f MB", bytes / 1048576.0)
}
