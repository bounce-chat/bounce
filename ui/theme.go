package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var colorNameOutgoingChatBubble = fyne.ThemeColorName("outgoingChatBubble")
var colorNameIncomingChatBubble = fyne.ThemeColorName("incomingChatBubble")
var colorNameDeviceLocal = fyne.ThemeColorName("deviceLocal")
var colorNameDeviceOffline = fyne.ThemeColorName("deviceOffline")
var colorNameDeviceOnline = fyne.ThemeColorName("deviceOnline")

var iconNameLogo = fyne.ThemeIconName("bounceLogo")

type forcedVariant struct {
	fyne.Theme

	variant fyne.ThemeVariant
}

func (f *forcedVariant) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	// UUID tags
	if len(name) > 5 && name[0:5] == "uuid:" {
		trimmed := string(name[5:len(name)])
		id, err := uuid.Parse(trimmed)
		if err != nil {
			log.WithFields(log.Fields{
				"name":    name,
				"trimmed": trimmed,
				"error":   err.Error(),
			}).Warn("invlid UUID color name")
			return color.RGBA{0xff, 0xff, 0xff, 0xff}
		}
		return uuidToColor(id)
	}

	// Custom names
	switch name {
	case colorNameOutgoingChatBubble:
		if f.variant == theme.VariantDark {
			return color.NRGBA{0, 0x2c, 0x94, 0xff}
		}
		return color.NRGBA{0xb5, 0xd0, 0xff, 0xff}
	case colorNameIncomingChatBubble:
		if f.variant == theme.VariantDark {
			return color.NRGBA{0x20, 0x20, 0x20, 0xff}
		}
		return color.NRGBA{0xdd, 0xdd, 0xdd, 0xff}
	case colorNameDeviceLocal:
		if f.variant == theme.VariantDark {
			return color.RGBA{0x38, 0x2a, 0xf7, 0xff}
		}
		return color.RGBA{0x38, 0x2a, 0xf7, 0xff}
	case colorNameDeviceOffline:
		if f.variant == theme.VariantDark {
			return color.RGBA{0xaa, 0xaa, 0xaa, 0xff}
		}
		return color.RGBA{0xaa, 0xaa, 0xaa, 0xff}
	case colorNameDeviceOnline:
		if f.variant == theme.VariantDark {
			return color.RGBA{0x2d, 0xc2, 0x39, 0xff}
		}
		return color.RGBA{0x2d, 0xc2, 0x39, 0xff}
	}

	// Match hyperlinks to text
	if name == theme.ColorNameHyperlink {
		return f.Theme.Color(theme.ColorNameForeground, f.variant)
	}

	// Default to the default theme
	return f.Theme.Color(name, f.variant)
}

func (f *forcedVariant) Icon(name fyne.ThemeIconName) fyne.Resource {
	if name == iconNameLogo {
		if f.variant == theme.VariantDark {
			return newEmbeddedResource("assets/logo_dark.png")
		} else {
			return newEmbeddedResource("assets/logo.png")
		}
	}

	return f.Theme.Icon(name)
}
