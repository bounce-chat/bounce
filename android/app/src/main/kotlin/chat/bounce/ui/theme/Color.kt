package chat.bounce.ui.theme

import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import chat.bounce.data.Avatars

/**
 * The palette from the desktop client's ui/theme.go. The two clients ship as one
 * product, so the brand hues are copied verbatim rather than re-derived.
 */
val BrandPrimary = Color(0xFF382AF7)
val BrandOnline = Color(0xFF2DC239)
val BrandOffline = Color(0xFFAAAAAA)
val BrandUnread = Color(0xFF217EFF)

private val OutgoingBubbleLight = Color(0xFFB5D0FF)
private val OutgoingBubbleDark = Color(0xFF002C94)
private val IncomingBubbleLight = Color(0xFFDDDDDD)
private val IncomingBubbleDark = Color(0xFF202020)
private val HyperlinkLight = Color(0xFF464656)
private val HyperlinkDark = Color(0xFFE0E0FF)
private val TypingDotIdle = Color(0xFF969696)
private val TypingDotActive = Color(0xFFE3E3E3)

/**
 * Chat colours that Material 3 has no role for.
 *
 * A bubble is not a surface, a container or a card - it carries meaning
 * (mine vs theirs) that must survive dynamic colour, so it cannot be folded into
 * [androidx.compose.material3.ColorScheme] without one of the two systems
 * lying. They travel beside the scheme in [LocalBounceColors] instead.
 */
@Immutable
data class BounceColors(
    val outgoingBubble: Color,
    val onOutgoingBubble: Color,
    val incomingBubble: Color,
    val onIncomingBubble: Color,
    val online: Color,
    val offline: Color,
    val deviceLocal: Color,
    val unreadBadge: Color,
    val onUnreadBadge: Color,
    val hyperlink: Color,
    val typingDotIdle: Color,
    val typingDotActive: Color,
)

val LightBounceColors = BounceColors(
    outgoingBubble = OutgoingBubbleLight,
    onOutgoingBubble = Color(0xFF071033),
    incomingBubble = IncomingBubbleLight,
    onIncomingBubble = Color(0xFF1B1B1F),
    online = BrandOnline,
    offline = BrandOffline,
    deviceLocal = BrandPrimary,
    unreadBadge = BrandUnread,
    onUnreadBadge = Color.White,
    hyperlink = HyperlinkLight,
    typingDotIdle = TypingDotIdle,
    typingDotActive = Color(0xFF4A4A4A),
)

val DarkBounceColors = BounceColors(
    outgoingBubble = OutgoingBubbleDark,
    onOutgoingBubble = Color(0xFFE6ECFF),
    incomingBubble = IncomingBubbleDark,
    onIncomingBubble = Color(0xFFE5E1E6),
    online = BrandOnline,
    offline = BrandOffline,
    deviceLocal = BrandPrimary,
    unreadBadge = BrandUnread,
    onUnreadBadge = Color.White,
    hyperlink = HyperlinkDark,
    typingDotIdle = TypingDotIdle,
    typingDotActive = TypingDotActive,
)

/**
 * Defaults to the light set so a preview or a stray composable outside
 * [BounceTheme] renders something legible instead of throwing.
 */
val LocalBounceColors = staticCompositionLocalOf { LightBounceColors }

internal val BounceLightColorScheme = lightColorScheme(
    primary = BrandPrimary,
    onPrimary = Color.White,
    primaryContainer = Color(0xFFE2DFFF),
    onPrimaryContainer = Color(0xFF14006E),
    inversePrimary = Color(0xFFC1C1FF),
    secondary = Color(0xFF5D5C72),
    onSecondary = Color.White,
    secondaryContainer = Color(0xFFE2E0F9),
    onSecondaryContainer = Color(0xFF1A1A2C),
    tertiary = Color(0xFF79536A),
    onTertiary = Color.White,
    tertiaryContainer = Color(0xFFFFD8EC),
    onTertiaryContainer = Color(0xFF2E1125),
    error = Color(0xFFBA1A1A),
    onError = Color.White,
    errorContainer = Color(0xFFFFDAD6),
    onErrorContainer = Color(0xFF410002),
    background = Color(0xFFFFFBFF),
    onBackground = Color(0xFF1C1B1F),
    surface = Color(0xFFFFFBFF),
    onSurface = Color(0xFF1C1B1F),
    surfaceVariant = Color(0xFFE4E1EC),
    onSurfaceVariant = Color(0xFF47464F),
    surfaceTint = BrandPrimary,
    inverseSurface = Color(0xFF313034),
    inverseOnSurface = Color(0xFFF3EFF4),
    outline = Color(0xFF787680),
    outlineVariant = Color(0xFFC8C5D0),
    scrim = Color.Black,
)

internal val BounceDarkColorScheme = darkColorScheme(
    primary = Color(0xFFC1C1FF),
    onPrimary = Color(0xFF1E00A8),
    primaryContainer = Color(0xFF2C15DE),
    onPrimaryContainer = Color(0xFFE2DFFF),
    inversePrimary = BrandPrimary,
    secondary = Color(0xFFC6C4DD),
    onSecondary = Color(0xFF2F2F42),
    secondaryContainer = Color(0xFF454559),
    onSecondaryContainer = Color(0xFFE2E0F9),
    tertiary = Color(0xFFE9B9D3),
    onTertiary = Color(0xFF46263A),
    tertiaryContainer = Color(0xFF5F3C51),
    onTertiaryContainer = Color(0xFFFFD8EC),
    error = Color(0xFFFFB4AB),
    onError = Color(0xFF690005),
    errorContainer = Color(0xFF93000A),
    onErrorContainer = Color(0xFFFFDAD6),
    background = Color(0xFF121212),
    onBackground = Color(0xFFE5E1E6),
    surface = Color(0xFF121212),
    onSurface = Color(0xFFE5E1E6),
    surfaceVariant = Color(0xFF47464F),
    onSurfaceVariant = Color(0xFFC8C5D0),
    surfaceTint = Color(0xFFC1C1FF),
    inverseSurface = Color(0xFFE5E1E6),
    inverseOnSurface = Color(0xFF313034),
    outline = Color(0xFF928F9A),
    outlineVariant = Color(0xFF47464F),
    scrim = Color.Black,
)

/**
 * The desktop client's uuidToColor (ui/default_image.go) as a Compose [Color]:
 * a contact's generated avatar and their group-chat name colour have to match
 * across platforms, since that colour is often the only thing distinguishing two
 * people with the same initial.
 *
 * The algorithm itself lives in data/Avatars.kt, because notification icons and
 * shortcuts need it far from any composition.
 */
fun uuidColor(id: String): Color = Color(Avatars.colorForId(id))
