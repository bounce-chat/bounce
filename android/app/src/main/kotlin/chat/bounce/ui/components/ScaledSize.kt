package chat.bounce.ui.components

import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.TextUnit

/**
 * An sp measurement resolved to dp, so a decoration tracks the system font scale
 * the way the text beside it does.
 *
 * This is only about the user's font size setting. Screen density needs no help
 * - that is what dp already means - so an icon sized in plain dp is correct
 * across every DPI and wrong only when someone turns their font up, at which
 * point the text grows and the glyph pinned next to it does not.
 *
 * At the default font scale this returns exactly the sp value in dp, so
 * switching a fixed size over to it changes nothing for most users.
 *
 * @param max ceiling on the result. Required rather than optional because
 *   accessibility font scales reach 2x, and a decoration that doubles stops
 *   being a decoration - a 12sp glyph at 24dp crowds the row it is a footnote
 *   in. Capping keeps the useful part of the range, where most people sit, and
 *   gives up only the extreme.
 */
@Composable
@ReadOnlyComposable
fun TextUnit.scaledDp(max: Dp): Dp =
    with(LocalDensity.current) { toDp() }.coerceAtMost(max)
