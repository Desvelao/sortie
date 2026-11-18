package app.sortie.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

private val DarkColors = darkColorScheme(
    primary = Color(0xFFB4E33D),
    onPrimary = Color(0xFF243300),
    primaryContainer = Color(0xFF384D00),
    onPrimaryContainer = Color(0xFFD0FF5E),
    secondaryContainer = Color(0xFF2E3326),
    onSecondaryContainer = Color(0xFFDDE5C8),
    background = Color(0xFF0E1116),
    onBackground = Color(0xFFE3E6DA),
    surface = Color(0xFF0E1116),
    onSurface = Color(0xFFE3E6DA),
    surfaceVariant = Color(0xFF2A2F25),
    onSurfaceVariant = Color(0xFFC5CCB4),
    surfaceContainer = Color(0xFF181C14),
    surfaceContainerHigh = Color(0xFF22271C),
    outline = Color(0xFF8A917A),
    outlineVariant = Color(0xFF43483A),
    errorContainer = Color(0xFF5A1D0B),
    onErrorContainer = Color(0xFFFFDBCF),
)

private val LightColors = lightColorScheme(
    primary = Color(0xFF4D6B00),
    onPrimary = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFFDDEAB8),
    onPrimaryContainer = Color(0xFF161F00),
    secondaryContainer = Color(0xFFEEF2E2),
    onSecondaryContainer = Color(0xFF1B1D17),
    background = Color(0xFFF8FAF1),
    onBackground = Color(0xFF1B1D17),
    surface = Color(0xFFF8FAF1),
    onSurface = Color(0xFF1B1D17),
    surfaceVariant = Color(0xFFDFE5CF),
    onSurfaceVariant = Color(0xFF43483A),
    surfaceContainer = Color(0xFFEEF2E2),
    surfaceContainerHigh = Color(0xFFE6EBD8),
    outline = Color(0xFF727866),
    outlineVariant = Color(0xFFC5CCB4),
    errorContainer = Color(0xFFFFEDE6),
    onErrorContainer = Color(0xFF7A2A12),
)

private val AppShapes = Shapes(
    extraSmall = RoundedCornerShape(8.dp),
    small = RoundedCornerShape(12.dp),
    medium = RoundedCornerShape(16.dp),
    large = RoundedCornerShape(24.dp),
    extraLarge = RoundedCornerShape(28.dp),
)

/** App-wide Material 3 theme; follows the system dark/light setting. */
@Composable
fun AppTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = if (isSystemInDarkTheme()) DarkColors else LightColors,
        shapes = AppShapes,
        content = content,
    )
}
