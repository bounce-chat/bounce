package ui

import (
	"image"
	"testing"

	"github.com/alecthomas/assert/v2"
)

func square(side int) image.Image {
	return image.NewRGBA(image.Rect(0, 0, side, side))
}

func TestThumbnailCacheDropsTheLeastRecentlyShownFirst(t *testing.T) {
	// Room for three 10x10 thumbnails of 400 bytes each
	c := newThumbnailCache(1200)
	c.put("a", square(10))
	c.put("b", square(10))
	c.put("c", square(10))

	_, ok := c.get("a") // a is now the most recently shown
	assert.True(t, ok)

	c.put("d", square(10))
	_, ok = c.get("b")
	assert.False(t, ok, "the least recently shown thumbnail survived")
	for _, key := range []string{"a", "c", "d"} {
		_, ok = c.get(key)
		assert.True(t, ok, "a recently shown thumbnail was dropped: "+key)
	}
	assert.Equal(t, 1200, c.used)
}

func TestThumbnailCacheAccountsForReplacementsAndRemovals(t *testing.T) {
	c := newThumbnailCache(10000)
	c.put("a", square(10))
	c.put("a", square(20))
	assert.Equal(t, 1600, c.used, "replacing an entry double counted it")
	assert.Equal(t, 1, c.order.Len())

	c.remove("a")
	c.remove("a")
	assert.Equal(t, 0, c.used)
	_, ok := c.get("a")
	assert.False(t, ok)

	// A single thumbnail larger than the whole budget is still kept, so a bubble can always show something
	c = newThumbnailCache(100)
	c.put("big", square(10))
	_, ok = c.get("big")
	assert.True(t, ok)
}

func TestScaledHeightToleratesClaimedDimensions(t *testing.T) {
	assert.Equal(t, 150, scaledHeight(300, 400, 200))
	assert.Equal(t, 300, scaledHeight(300, 0, 0), "zero dimensions should fall back to a square")
	assert.Equal(t, 300, scaledHeight(300, -1, 50), "negative dimensions should fall back to a square")
	assert.Equal(t, 3000, scaledHeight(300, 1, 100000), "an absurd aspect ratio should be capped")
	assert.Equal(t, 1, scaledHeight(300, 100000, 1), "a very wide image still gets a row of pixels")
}

func opaque(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
	return img
}

func TestFitThumbnailKeepsProportionsAndLetterboxesTheVeryTall(t *testing.T) {
	landscape := fitThumbnail(opaque(400, 200), 300)
	assert.Equal(t, image.Rect(0, 0, 300, 150), landscape.Bounds())
	assert.Equal(t, uint8(0xff), landscape.Pix[3], "a picture that fits should fill its thumbnail")

	// Fifty times taller than wide: the thumbnail is capped at ten times and the picture is centred in it
	tall := fitThumbnail(opaque(10, 500), 300)
	assert.Equal(t, image.Rect(0, 0, 300, 3000), tall.Bounds())
	assert.Equal(t, uint8(0), tall.RGBAAt(0, 1500).A, "the letterbox should be transparent")
	assert.Equal(t, uint8(0), tall.RGBAAt(299, 1500).A, "the letterbox should be transparent")
	assert.Equal(t, uint8(0xff), tall.RGBAAt(150, 1500).A, "the picture should be in the middle")
}
