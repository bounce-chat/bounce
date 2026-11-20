// Package assets embeds Bounce's icons and logos so a UI can load them from the binary.  The directory is otherwise
// just the image files: the packaging scripts and the README read them from here by path.
package assets

import "embed"

// FS holds every image in this directory.  Paths are relative to it: "icon.png", "icons/menu/about.png".
//
//go:embed *.png *.svg icons
var FS embed.FS
