package chat.bounce.ui.theme

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.unit.dp
import androidx.core.view.WindowCompat

/** ui/theme.go pins buttons, inputs and selections at a 12dp radius. */
val BounceShapes = Shapes(
    extraSmall = RoundedCornerShape(8.dp),
    small = RoundedCornerShape(12.dp),
    medium = RoundedCornerShape(12.dp),
    large = RoundedCornerShape(16.dp),
    extraLarge = RoundedCornerShape(24.dp),
)

/**
 * @param dynamicColor honour the wallpaper palette on Android 12+. When off (or
 *   unsupported) the brand scheme is used, so Bounce still looks like Bounce.
 */
@Composable
fun BounceTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    dynamicColor: Boolean = true,
    content: @Composable () -> Unit,
) {
    val context = LocalContext.current
    val colorScheme = when {
        dynamicColor && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S ->
            if (darkTheme) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)

        darkTheme -> BounceDarkColorScheme
        else -> BounceLightColorScheme
    }

    // The chat colours are brand identity, not accent: they stay put even when
    // the scheme above has been recoloured from the wallpaper.
    val bounceColors = if (darkTheme) DarkBounceColors else LightBounceColors

    val view = LocalView.current
    if (!view.isInEditMode) {
        SideEffect {
            val window = view.context.findActivity()?.window ?: return@SideEffect
            WindowCompat.getInsetsController(window, view).apply {
                isAppearanceLightStatusBars = !darkTheme
                isAppearanceLightNavigationBars = !darkTheme
            }
        }
    }

    CompositionLocalProvider(LocalBounceColors provides bounceColors) {
        MaterialTheme(
            colorScheme = colorScheme,
            typography = BounceTypography,
            shapes = BounceShapes,
            content = content,
        )
    }
}

/** Accessor for the colours Material 3 has no role for: `BounceTheme.colors.outgoingBubble`. */
object BounceTheme {
    val colors: BounceColors
        @Composable
        @ReadOnlyComposable
        get() = LocalBounceColors.current
}

private tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}
