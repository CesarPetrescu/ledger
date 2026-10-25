package com.cesarpetrescu.ledger

import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontVariation
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

// Ledger's design tokens. They are the web console's (frontend/src/styles.css): paper ground, ink text,
// one blue, and Sora for names and numbers. Screens read these; a raw colour or size in a screen is a bug.

/** The screen's side margin. */
val Gutter = 16.dp

/** Buttons have 8 dp corners, like cards; Material's default pill reads as another product. */
val ButtonShape = RoundedCornerShape(8.dp)

/** What a colour means. Each has a soft background and a readable foreground in both themes. */
enum class Tone { Neutral, Accent, Warn, Danger, Good, Research }

@Immutable
class LedgerColors(val body: Color, val star: Color, private val tones: Map<Tone, Pair<Color, Color>>) {
    /** A tone's background and text colours. */
    fun tone(tone: Tone): Pair<Color, Color> = tones.getValue(tone)
}

private val LightLedger = LedgerColors(body = Color(0xFF313C50), star = Color(0xFFB88A00), tones = mapOf(
    Tone.Neutral to (Color(0xFFF4F6F8) to Color(0xFF485367)),
    Tone.Accent to (Color(0xFFE9F1FF) to Color(0xFF145FC8)),
    Tone.Warn to (Color(0xFFFFF3DC) to Color(0xFF8A5A06)),
    Tone.Danger to (Color(0xFFFFF0F1) to Color(0xFFB4232F)),
    Tone.Good to (Color(0xFFE7F5EE) to Color(0xFF1F7A50)),
    Tone.Research to (Color(0xFFF2EAFD) to Color(0xFF6B3FA0)),
))

private val DarkLedger = LedgerColors(body = Color(0xFFD5DCE6), star = Color(0xFFF2C14E), tones = mapOf(
    Tone.Neutral to (Color(0xFF1D2430) to Color(0xFFC2CAD6)),
    Tone.Accent to (Color(0xFF1A2940) to Color(0xFF93BDFF)),
    Tone.Warn to (Color(0xFF3A2C13) to Color(0xFFF3C46E)),
    Tone.Danger to (Color(0xFF3A1D22) to Color(0xFFFF8A93)),
    Tone.Good to (Color(0xFF163326) to Color(0xFF82D8AA)),
    Tone.Research to (Color(0xFF2C2140) to Color(0xFFC9A8F5)),
))

// Every role is set, so nothing falls back to Material's stock lavender. Surface containers are what
// cards, sheets, menus, dialogs and the navigation bar draw on: white on paper, a step lighter in the dark.
private val LightColors = lightColorScheme(
    primary = Color(0xFF1769E0), onPrimary = Color.White, primaryContainer = Color(0xFFE9F1FF), onPrimaryContainer = Color(0xFF145FC8),
    inversePrimary = Color(0xFF74AAFF),
    secondary = Color(0xFF485367), onSecondary = Color.White, secondaryContainer = Color(0xFFE9F1FF), onSecondaryContainer = Color(0xFF145FC8),
    tertiary = Color(0xFF8A5A06), onTertiary = Color.White, tertiaryContainer = Color(0xFFFFF3DC), onTertiaryContainer = Color(0xFF8A5A06),
    background = Color(0xFFFBFAF7), onBackground = Color(0xFF172033),
    surface = Color(0xFFFBFAF7), onSurface = Color(0xFF172033), surfaceVariant = Color(0xFFF4F6F8), onSurfaceVariant = Color(0xFF687386),
    surfaceTint = Color(0xFF1769E0), inverseSurface = Color(0xFF172033), inverseOnSurface = Color(0xFFE7ECF3),
    error = Color(0xFFB4232F), onError = Color.White, errorContainer = Color(0xFFFFF0F1), onErrorContainer = Color(0xFF7D1A24),
    outline = Color(0xFFCBD1D8), outlineVariant = Color(0xFFE1E4E8), scrim = Color.Black,
    surfaceBright = Color.White, surfaceDim = Color(0xFFF1EFEA),
    surfaceContainerLowest = Color.White, surfaceContainerLow = Color.White, surfaceContainer = Color.White,
    surfaceContainerHigh = Color.White, surfaceContainerHighest = Color(0xFFECEFF2),
)

private val DarkColors = darkColorScheme(
    primary = Color(0xFF74AAFF), onPrimary = Color(0xFF0B1220), primaryContainer = Color(0xFF1A2940), onPrimaryContainer = Color(0xFF93BDFF),
    inversePrimary = Color(0xFF1769E0),
    secondary = Color(0xFFC2CAD6), onSecondary = Color(0xFF0F141B), secondaryContainer = Color(0xFF1A2940), onSecondaryContainer = Color(0xFF93BDFF),
    tertiary = Color(0xFFF3C46E), onTertiary = Color(0xFF0F141B), tertiaryContainer = Color(0xFF3A2C13), onTertiaryContainer = Color(0xFFF3C46E),
    background = Color(0xFF0F141B), onBackground = Color(0xFFE7ECF3),
    surface = Color(0xFF0F141B), onSurface = Color(0xFFE7ECF3), surfaceVariant = Color(0xFF1D2430), onSurfaceVariant = Color(0xFFA0ABBC),
    surfaceTint = Color(0xFF74AAFF), inverseSurface = Color(0xFFE7ECF3), inverseOnSurface = Color(0xFF172033),
    error = Color(0xFFFF8A93), onError = Color(0xFF0F141B), errorContainer = Color(0xFF3A1D22), onErrorContainer = Color(0xFFFFC2C7),
    outline = Color(0xFF3A4556), outlineVariant = Color(0xFF283140), scrim = Color.Black,
    surfaceBright = Color(0xFF242C39), surfaceDim = Color(0xFF0B1016),
    surfaceContainerLowest = Color(0xFF161C25), surfaceContainerLow = Color(0xFF161C25), surfaceContainer = Color(0xFF161C25),
    surfaceContainerHigh = Color(0xFF1D2430), surfaceContainerHighest = Color(0xFF242C39),
)

@OptIn(androidx.compose.ui.text.ExperimentalTextApi::class)
private fun sora(weight: Int) = Font(R.font.sora, FontWeight(weight), variationSettings = FontVariation.Settings(FontVariation.weight(weight)))

/** Sora, the web console's typeface, bundled as one variable font (SIL Open Font License, see assets/fonts). */
val Sora = FontFamily(sora(400), sora(500), sora(600), sora(700))

// Sora for titles, names, numbers and navigation; the system's Roboto for reading text, tuned for small sizes.
private val LedgerType = Typography(
    displaySmall = TextStyle(fontFamily = Sora, fontWeight = FontWeight.SemiBold, fontSize = 28.sp, lineHeight = 34.sp),
    headlineSmall = TextStyle(fontFamily = Sora, fontWeight = FontWeight.SemiBold, fontSize = 22.sp, lineHeight = 29.sp),
    titleLarge = TextStyle(fontFamily = Sora, fontWeight = FontWeight.SemiBold, fontSize = 19.sp, lineHeight = 25.sp),
    titleMedium = TextStyle(fontFamily = Sora, fontWeight = FontWeight.SemiBold, fontSize = 16.sp, lineHeight = 22.sp),
    titleSmall = TextStyle(fontWeight = FontWeight.Medium, fontSize = 15.sp, lineHeight = 20.sp),
    bodyLarge = TextStyle(fontSize = 16.sp, lineHeight = 23.sp),
    bodyMedium = TextStyle(fontSize = 15.sp, lineHeight = 22.sp),
    bodySmall = TextStyle(fontSize = 13.sp, lineHeight = 18.sp),
    labelLarge = TextStyle(fontWeight = FontWeight.Medium, fontSize = 14.sp, lineHeight = 20.sp),
    labelMedium = TextStyle(fontFamily = Sora, fontWeight = FontWeight.Medium, fontSize = 12.sp, lineHeight = 16.sp),
    labelSmall = TextStyle(fontWeight = FontWeight.Medium, fontSize = 12.sp, lineHeight = 16.sp),
)

private val LedgerShapes = Shapes(
    extraSmall = RoundedCornerShape(4.dp), small = RoundedCornerShape(6.dp), medium = RoundedCornerShape(8.dp),
    large = RoundedCornerShape(12.dp), extraLarge = RoundedCornerShape(16.dp),
)

private val LocalLedgerColors = staticCompositionLocalOf { LightLedger }

/** Ledger's colours beyond Material's roles: reading text, the star, and the state tones. */
val ledgerColors: LedgerColors @Composable get() = LocalLedgerColors.current

@Composable
fun LedgerTheme(dark: Boolean, content: @Composable () -> Unit) {
    CompositionLocalProvider(LocalLedgerColors provides if (dark) DarkLedger else LightLedger) {
        MaterialTheme(colorScheme = if (dark) DarkColors else LightColors, typography = LedgerType, shapes = LedgerShapes, content = content)
    }
}
