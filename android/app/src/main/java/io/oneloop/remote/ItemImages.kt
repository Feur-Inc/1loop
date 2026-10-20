package io.oneloop.remote

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

// The pictures of an item: the screenshots a tool returned to the model (or
// those attached to a message), as the computer shows them. Kept decoded
// for a few screens, at most 1600 px on their longest side.
private val decoded = object : android.util.LruCache<String, ImageBitmap>(48 shl 20) {
    override fun sizeOf(key: String, value: ImageBitmap) = value.width * value.height * 4
}

/** Picture n of the open chat's item at index (in the whole chat), or null. */
suspend fun RemoteApp.itemImage(index: Int, n: Int): ImageBitmap? {
    val target = server ?: return null
    val chat = current ?: return null
    val key = "${target.key}/$chat/$index/$n"
    decoded.get(key)?.let { return it }
    val image = withContext(Dispatchers.IO) {
        runCatching {
            val bytes = Api(target).bytes("/api/chats/$chat/items/$index/images/$n")
            val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
            var sample = 1
            while (maxOf(bounds.outWidth, bounds.outHeight) / sample > 1600) sample *= 2
            BitmapFactory.decodeByteArray(bytes, 0, bytes.size, BitmapFactory.Options().apply { inSampleSize = sample })?.asImageBitmap()
        }.getOrNull()
    } ?: return null
    decoded.put(key, image)
    return image
}

/** Thumbnails of an item's pictures; a tap shows one full screen. */
@Composable
fun ItemImages(app: RemoteApp, index: Int, count: Int, modifier: Modifier = Modifier) {
    var open by remember { mutableStateOf<ImageBitmap?>(null) }
    Row(modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        for (n in 0 until count) {
            val image by produceState<ImageBitmap?>(null, app.server, app.current, index, n) { value = app.itemImage(index, n) }
            Box(
                Modifier.height(140.dp).widthIn(min = 90.dp, max = 280.dp).clip(RoundedCornerShape(10.dp))
                    .background(Palette.code).border(1.dp, Palette.borderSoft, RoundedCornerShape(10.dp))
                    .clickable(enabled = image != null) { open = image },
                contentAlignment = Alignment.Center,
            ) {
                image?.let { Image(it, contentDescription = "Image the model saw", contentScale = ContentScale.Fit, modifier = Modifier.height(140.dp)) }
                    ?: WorkRing("wait", 22.dp)
            }
        }
    }
    open?.let { image ->
        Dialog(onDismissRequest = { open = null }, properties = DialogProperties(usePlatformDefaultWidth = false)) {
            Box(Modifier.fillMaxSize().background(Color.Black).clickable { open = null }.padding(8.dp), contentAlignment = Alignment.Center) {
                Image(image, contentDescription = "Image the model saw", contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize())
            }
        }
    }
}
