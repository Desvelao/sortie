package app.sortie.ui.previews

import android.media.MediaPlayer
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import app.sortie.R
import app.sortie.core.FileEntry
import app.sortie.data.Storage
import app.sortie.media.Metadata
import app.sortie.media.Pictures
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

private val TEXT_EXT = setOf(".txt", ".md", ".csv", ".json", ".xml", ".yaml", ".yml", ".log", ".ini", ".html", ".htm", ".srt", ".tsv", ".kt", ".py", ".sh")
private const val TEXT_LIMIT = 64 * 1024

/** What the file looks like: a picture, its text, its audio tags, or its details with a way to open it. */
@Composable
fun Preview(file: FileEntry, metadata: suspend (FileEntry) -> Map<String, String>, modifier: Modifier = Modifier) {
    val ext = file.ext
    val kind = when {
        ext == ".pdf" || Metadata.isImage(ext) || file.mime.startsWith("image/") -> Kind.PICTURE
        Metadata.isVideo(ext) -> Kind.VIDEO
        Metadata.isAudio(ext) -> Kind.AUDIO
        ext in TEXT_EXT || file.mime.startsWith("text/") -> Kind.TEXT
        else -> Kind.OTHER
    }
    androidx.compose.material3.Surface(modifier.fillMaxSize(), shape = MaterialTheme.shapes.extraLarge, color = MaterialTheme.colorScheme.surfaceContainer) {
    Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally) {
        Text(file.name, style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp), maxLines = 2)
        when (kind) {
            Kind.PICTURE -> PictureBox(file, Modifier.weight(1f))
            Kind.TEXT -> TextBox(file, Modifier.weight(1f))
            Kind.VIDEO -> Column(Modifier.weight(1f), horizontalAlignment = Alignment.CenterHorizontally) {
                PictureBox(file, Modifier.weight(1f))
                OpenWithButton(file)
                Details(file, metadata)
            }
            Kind.AUDIO -> AudioBox(file, metadata, Modifier.weight(1f))
            Kind.OTHER -> Column(Modifier.weight(1f), horizontalAlignment = Alignment.CenterHorizontally) {
                Icon(painterResource(R.drawable.ic_file), null, Modifier.size(96.dp).padding(16.dp))
                OpenWithButton(file)
                Details(file, metadata)
            }
        }
    }
    }
}

private enum class Kind { PICTURE, TEXT, VIDEO, AUDIO, OTHER }

@Composable
private fun loadPicture(file: FileEntry, maxPx: Int): ImageBitmap? {
    val state = produceState<ImageBitmap?>(null, file.path, file.size, file.modTimeMs) {
        value = withContext(Dispatchers.IO) { Pictures.load(file, maxPx)?.asImageBitmap() }
    }
    return state.value
}

@Composable
private fun PictureBox(file: FileEntry, modifier: Modifier) {
    val bmp = loadPicture(file, 1600)
    if (bmp != null) {
        Image(bmp, file.name, modifier.fillMaxWidth(), contentScale = ContentScale.Fit)
    } else {
        Icon(painterResource(R.drawable.ic_file), stringResource(R.string.no_preview), modifier.size(96.dp).padding(16.dp))
    }
}

@Composable
private fun TextBox(file: FileEntry, modifier: Modifier) {
    val text by produceState("", file.path, file.modTimeMs) {
        value = withContext(Dispatchers.IO) {
            runCatching {
                File(file.path).inputStream().use { s ->
                    val buf = ByteArray(TEXT_LIMIT)
                    var n = 0
                    while (n < buf.size) {
                        val r = s.read(buf, n, buf.size - n)
                        if (r <= 0) break
                        n += r
                    }
                    String(buf, 0, n, Charsets.UTF_8)
                }
            }.getOrDefault("")
        }
    }
    Text(
        text,
        modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(16.dp),
        fontFamily = FontFamily.Monospace,
        style = MaterialTheme.typography.bodySmall,
    )
}

@Composable
private fun OpenWithButton(file: FileEntry) {
    val context = LocalContext.current
    Button(onClick = { Storage.openWith(context, file) }, modifier = Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.open_with)) }
}

@Composable
private fun Details(file: FileEntry, metadata: suspend (FileEntry) -> Map<String, String>) {
    val fields by produceState<Map<String, String>>(emptyMap(), file.path, file.modTimeMs) { value = metadata(file) }
    Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(stringResource(R.string.file_size, android.text.format.Formatter.formatShortFileSize(LocalContext.current, file.size)), style = MaterialTheme.typography.bodySmall)
        for ((k, v) in fields.toSortedMap()) Row { Text("$k: $v", style = MaterialTheme.typography.bodySmall) }
    }
}

@Composable
private fun AudioBox(file: FileEntry, metadata: suspend (FileEntry) -> Map<String, String>, modifier: Modifier) {
    var playing by remember(file.path) { mutableStateOf(false) }
    val player = remember(file.path) { arrayOfNulls<MediaPlayer>(1) }
    DisposableEffect(file.path) {
        onDispose {
            runCatching { player[0]?.release() }
            player[0] = null
        }
    }
    Column(modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
        PictureBox(file, Modifier.weight(1f))
        Button(
            modifier = Modifier.heightIn(min = 48.dp),
            onClick = {
                val p = player[0] ?: MediaPlayer().also {
                    it.setDataSource(file.path)
                    it.prepare()
                    it.setOnCompletionListener { playing = false }
                    player[0] = it
                }
                if (p.isPlaying) {
                    p.pause()
                    playing = false
                } else {
                    p.start()
                    playing = true
                }
            },
        ) {
            if (playing) Icon(painterResource(R.drawable.ic_pause), null) else Icon(Icons.Filled.PlayArrow, null)
            Text(stringResource(if (playing) R.string.pause else R.string.play), Modifier.padding(start = 8.dp))
        }
        Details(file, metadata)
    }
}
