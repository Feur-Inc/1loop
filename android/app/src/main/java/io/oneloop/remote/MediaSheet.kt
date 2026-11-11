// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.Manifest
import android.annotation.SuppressLint
import android.content.ActivityNotFoundException
import android.content.ContentUris
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.BitmapFactory
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import android.util.LruCache
import android.util.Size
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.material3.ripple
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.core.content.FileProvider
import androidx.core.content.edit
import androidx.lifecycle.compose.LifecycleResumeEffect
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

// The composer's +, as in Discord (#241): a sheet of the phone's recent
// photos and videos to pick several at once, with the file picker, Android's
// photo picker and the camera above them. The grid needs access to the
// photos; refused, the buttons stay.

/** How much of the phone's photos and videos 1loop may list. */
enum class MediaAccess { All, Selected, None }

/** The permissions that list the photos and videos on this Android version. */
@SuppressLint("InlinedApi") // each one only where sdk has it
fun mediaPermissions(sdk: Int): Array<String> = when {
    sdk >= 34 -> arrayOf(Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO, Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED)
    sdk >= 33 -> arrayOf(Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO)
    else -> arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE)
}

/** Android 14's "Select photos" grants only READ_MEDIA_VISUAL_USER_SELECTED: the grid shows those. */
@SuppressLint("InlinedApi")
fun mediaAccess(sdk: Int, granted: (String) -> Boolean): MediaAccess = when {
    sdk >= 33 && (granted(Manifest.permission.READ_MEDIA_IMAGES) || granted(Manifest.permission.READ_MEDIA_VIDEO)) -> MediaAccess.All
    sdk >= 34 && granted(Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED) -> MediaAccess.Selected
    sdk < 33 && granted(Manifest.permission.READ_EXTERNAL_STORAGE) -> MediaAccess.All
    else -> MediaAccess.None
}

private fun mediaAccess(context: Context) = mediaAccess(Build.VERSION.SDK_INT) {
    ContextCompat.checkSelfPermission(context, it) == PackageManager.PERMISSION_GRANTED
}

/** A video's length on its tile: 0:07, 12:30, 1:02:03. */
fun durationLabel(millis: Long): String {
    val seconds = millis / 1000
    val (h, m, s) = Triple(seconds / 3600, seconds / 60 % 60, seconds % 60)
    return if (h > 0) "%d:%02d:%02d".format(h, m, s) else "%d:%02d".format(m, s)
}

/** Tapped again, a picked tile leaves the selection; the others keep their order (their numbers). */
fun <T> List<T>.toggled(item: T): List<T> = if (item in this) this - item else this + item

data class MediaItem(val uri: Uri, val video: Boolean, val duration: Long, val added: Long)

/** The newest photos and videos first; only their ids are read here, the tiles load their pictures. */
private fun recentMedia(context: Context, limit: Int = 600): List<MediaItem> {
    fun query(collection: Uri, video: Boolean): List<MediaItem> {
        val columns = buildList {
            add(MediaStore.MediaColumns._ID); add(MediaStore.MediaColumns.DATE_ADDED)
            if (video) add(MediaStore.Video.VideoColumns.DURATION)
        }.toTypedArray()
        val found = mutableListOf<MediaItem>()
        runCatching {
            context.contentResolver.query(collection, columns, null, null, "${MediaStore.MediaColumns.DATE_ADDED} DESC")?.use { cursor ->
                while (found.size < limit && cursor.moveToNext()) {
                    val id = cursor.getLong(0)
                    found += MediaItem(ContentUris.withAppendedId(collection, id), video, if (video) cursor.getLong(2) else 0, cursor.getLong(1))
                }
            }
        }
        return found
    }
    val images = query(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, false)
    val videos = query(MediaStore.Video.Media.EXTERNAL_CONTENT_URI, true)
    return (images + videos).sortedByDescending { it.added }.take(limit)
}

// Decoded tiles, kept while the sheet is closed and opened again.
private val tiles = LruCache<Uri, ImageBitmap>(240)

private fun loadTile(context: Context, item: MediaItem, pixels: Int): ImageBitmap? = runCatching {
    if (Build.VERSION.SDK_INT >= 29) return@runCatching context.contentResolver.loadThumbnail(item.uri, Size(pixels, pixels), null).asImageBitmap()
    if (item.video) return@runCatching null // shown with its length only
    val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
    context.contentResolver.openInputStream(item.uri)?.use { BitmapFactory.decodeStream(it, null, bounds) }
    var sample = 1
    while (bounds.outWidth / (sample * 2) >= pixels && bounds.outHeight / (sample * 2) >= pixels) sample *= 2
    context.contentResolver.openInputStream(item.uri)?.use {
        BitmapFactory.decodeStream(it, null, BitmapFactory.Options().apply { inSampleSize = sample })
    }?.asImageBitmap()
}.getOrNull()

/** The composer's +: the media sheet, and the pickers it opens (kept here, as the sheet closes before they answer). */
@Composable
fun AttachButton(app: RemoteApp, project: String?) {
    val context = LocalContext.current
    var open by remember { mutableStateOf(false) }
    val files = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) app.attach(uris, project)
    }
    val photos = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia()) { uris ->
        if (uris.isNotEmpty()) app.attach(uris, project)
    }
    // The camera writes into 1loop's cache, shared with it through the FileProvider.
    var shot by rememberSaveable { mutableStateOf<String?>(null) }
    val camera = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { taken ->
        val uri = shot?.toUri()
        if (taken && uri != null) app.attach(listOf(uri), project)
        shot = null
    }
    val takePhoto = {
        val folder = File(context.cacheDir, "camera").apply { mkdirs() }
        val file = File(folder, "Photo-${System.currentTimeMillis()}.jpg")
        val uri = FileProvider.getUriForFile(context, context.packageName + ".updates", file)
        shot = uri.toString()
        try {
            camera.launch(uri)
        } catch (e: SecurityException) {
            shot = null
            Toast.makeText(context, "1loop needs the camera permission to take a photo.", Toast.LENGTH_LONG).show()
        } catch (e: ActivityNotFoundException) {
            shot = null
            Toast.makeText(context, "No camera app could take the photo.", Toast.LENGTH_LONG).show()
        }
    }
    // The QR scanner library declares CAMERA in the manifest: Android then lets
    // the app start the camera app only once the permission is granted, and
    // otherwise throws instead of opening it.
    val cameraAccess = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) {
            takePhoto()
        } else {
            Toast.makeText(context, "1loop needs the camera permission to take a photo.", Toast.LENGTH_LONG).show()
        }
    }
    RoundButton(Icons.plus, Color.Transparent, Palette.muted, size = 32.dp, iconSize = 18.dp, description = "Attach files") { open = true }
    if (open) MediaSheet(
        onFiles = { open = false; files.launch(arrayOf("*/*")) },
        onPhotos = { open = false; photos.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageAndVideo)) },
        onCamera = if (Intent(MediaStore.ACTION_IMAGE_CAPTURE).resolveActivity(context.packageManager) == null) null else ({
            open = false
            val prefs = context.getSharedPreferences("1loop-media", Context.MODE_PRIVATE)
            val asked = prefs.getInt("cameraAsked", 0)
            when {
                context.checkSelfPermission(Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED -> takePhoto()
                asked < 2 -> { // Android stops asking after two refusals, as for the photos
                    prefs.edit { putInt("cameraAsked", asked + 1) }
                    cameraAccess.launch(Manifest.permission.CAMERA)
                }
                else -> {
                    Toast.makeText(context, "Allow the camera in 1loop's permissions to take a photo.", Toast.LENGTH_LONG).show()
                    context.startActivity(Intent(android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null)))
                }
            }
        }),
        onPicked = { open = false; app.attach(it, project) },
        onDone = { open = false },
    )
}

private fun String.toUri(): Uri = Uri.parse(this)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun MediaSheet(onFiles: () -> Unit, onPhotos: () -> Unit, onCamera: (() -> Unit)?, onPicked: (List<Uri>) -> Unit, onDone: () -> Unit) {
    val context = LocalContext.current
    val prefs = remember { context.getSharedPreferences("1loop-media", Context.MODE_PRIVATE) }
    var access by remember { mutableStateOf(mediaAccess(context)) }
    var reload by remember { mutableIntStateOf(0) }
    var media by remember { mutableStateOf<List<MediaItem>?>(null) }
    var picked by remember { mutableStateOf(emptyList<Uri>()) }
    val ask = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        access = mediaAccess(context)
        reload++ // "Select photos" again changes what is shown
    }
    val allow = {
        // Android stops asking after two refusals: the setting is then in the app's details.
        val asked = prefs.getInt("asked", 0)
        if (asked < 2 || access == MediaAccess.Selected) {
            prefs.edit { putInt("asked", asked + 1) }
            ask.launch(mediaPermissions(Build.VERSION.SDK_INT))
        } else {
            context.startActivity(Intent(android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null)))
        }
    }
    // Asked the first time the sheet opens; refused, the buttons are enough.
    LaunchedEffect(Unit) { if (access == MediaAccess.None && prefs.getInt("asked", 0) == 0) allow() }
    // Back from the app's settings, or a photo taken since.
    LifecycleResumeEffect(Unit) {
        access = mediaAccess(context)
        onPauseOrDispose {}
    }
    LaunchedEffect(access, reload) {
        media = if (access == MediaAccess.None) emptyList() else withContext(Dispatchers.IO) { recentMedia(context) }
        picked = picked.filter { uri -> media.orEmpty().any { it.uri == uri } }
    }
    GlassBackdrop()
    // Half the screen at first; dragged up, the grid takes all of it.
    ModalBottomSheet(onDismissRequest = onDone, containerColor = Palette.surface, scrimColor = GlassScrim, sheetState = rememberModalBottomSheetState()) {
        Column(Modifier.fillMaxSize().padding(horizontal = 12.dp)) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                SourceTile(Icons.document, "Files", Modifier.weight(1f), onFiles)
                SourceTile(Icons.image, "Photos", Modifier.weight(1f), onPhotos)
                if (onCamera != null) SourceTile(Icons.camera, "Camera", Modifier.weight(1f), onCamera)
            }
            Row(Modifier.fillMaxWidth().height(52.dp).padding(start = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("Recent", style = Type.small.copy(color = Palette.faint), modifier = Modifier.weight(1f))
                if (picked.isNotEmpty()) Pill("Attach ${picked.size}", primary = true) { onPicked(picked) }
            }
            when {
                access == MediaAccess.None -> AccessNote("Allow 1loop to see your photos and videos to pick recent ones here.", "Allow", allow)
                media?.isEmpty() == true -> AccessNote(
                    if (access == MediaAccess.Selected) "None of the photos you chose are left." else "No photos or videos on this phone yet.",
                    if (access == MediaAccess.Selected) "Choose photos" else null, allow,
                )
                else -> {
                    if (access == MediaAccess.Selected) AccessNote("1loop sees only the photos and videos you chose.", "Choose more", allow)
                    LazyVerticalGrid(
                        GridCells.Adaptive(104.dp), Modifier.weight(1f),
                        horizontalArrangement = Arrangement.spacedBy(3.dp), verticalArrangement = Arrangement.spacedBy(3.dp),
                    ) {
                        items(media.orEmpty(), key = { it.uri.toString() }) { item ->
                            MediaTile(item, picked.indexOf(item.uri)) { picked = picked.toggled(item.uri) }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun SourceTile(icon: ImageVector, label: String, modifier: Modifier, onClick: () -> Unit) {
    val press = remember { MutableInteractionSource() }
    Column(
        modifier.pressScale(press).clip(RoundedCornerShape(12.dp)).background(Palette.raised)
            .clickable(press, ripple(), onClick = onClick).padding(vertical = 12.dp),
        horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Icon(icon, null, tint = Palette.text, modifier = Modifier.size(22.dp))
        Text(label, style = Type.small.copy(color = Palette.text))
    }
}

@Composable
private fun AccessNote(text: String, action: String?, onAction: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().padding(bottom = 8.dp).clip(RoundedCornerShape(12.dp)).border(1.dp, Palette.border, RoundedCornerShape(12.dp))
            .padding(start = 14.dp, end = 6.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(text, style = Type.small, modifier = Modifier.weight(1f).padding(vertical = 8.dp))
        if (action != null) {
            Spacer(Modifier.size(8.dp))
            Text(
                action, style = Type.small.copy(color = Palette.amber, fontWeight = FontWeight.Medium),
                modifier = Modifier.clip(RoundedCornerShape(8.dp)).clickable(onClick = onAction).padding(horizontal = 10.dp, vertical = 10.dp),
            )
        }
    }
}

/** A photo or video of the grid; picked, it shows its number in the selection. */
@Composable
private fun MediaTile(item: MediaItem, index: Int, onClick: () -> Unit) {
    val context = LocalContext.current
    val pixels = with(LocalDensity.current) { 104.dp.roundToPx() }
    var image by remember(item.uri) { mutableStateOf<ImageBitmap?>(tiles.get(item.uri)) }
    LaunchedEffect(item.uri) {
        if (image == null) image = withContext(Dispatchers.IO) { loadTile(context, item, pixels) }?.also { tiles.put(item.uri, it) }
    }
    val selected = index >= 0
    Box(
        Modifier.aspectRatio(1f).clip(RoundedCornerShape(4.dp)).background(Palette.code).clickable(onClick = onClick),
    ) {
        image?.let { Image(it, null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize()) }
            ?: Icon(if (item.video) Icons.play else Icons.image, null, tint = Palette.faint, modifier = Modifier.size(22.dp).align(Alignment.Center))
        if (selected) Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.35f)).border(2.dp, Palette.amber, RoundedCornerShape(4.dp)))
        if (item.video) Row(
            Modifier.align(Alignment.BottomStart).padding(5.dp).clip(RoundedCornerShape(6.dp)).background(Color.Black.copy(alpha = 0.6f))
                .padding(horizontal = 5.dp, vertical = 2.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.play, null, tint = Color.White, modifier = Modifier.size(11.dp))
            Spacer(Modifier.size(3.dp))
            Text(durationLabel(item.duration), style = Type.caption.copy(color = Color.White))
        }
        // The selection's counter: an empty ring to tap, or the tile's number.
        Box(
            Modifier.align(Alignment.TopEnd).padding(6.dp).size(24.dp).clip(CircleShape)
                .background(if (selected) Palette.amber else Color.Black.copy(alpha = 0.25f))
                .border(1.5.dp, if (selected) Palette.amber else Color.White.copy(alpha = 0.85f), CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            if (selected) Text("${index + 1}", style = Type.caption.copy(color = Palette.ink, fontWeight = FontWeight.SemiBold))
        }
    }
}
