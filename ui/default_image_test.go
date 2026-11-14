package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"github.com/alecthomas/assert/v2"
	"github.com/bounce-chat/bounce/chat"
)

// bareDefaultImage is a defaultImage with no renderer behind it, pointed at the given id with no load
// in flight, enough to exercise how a finished load decides whether it still applies
func bareDefaultImage(id chat.ID) *defaultImage {
	return &defaultImage{
		id:              id,
		size:            32,
		backgroundColor: &canvas.Image{},
		foregroundText:  &canvas.Text{Text: "A", Color: color.White},
	}
}

func twoPixelPNG(t *testing.T) []byte {
	var buf bytes.Buffer
	assert.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	return buf.Bytes()
}

// The test driver runs fyne.Do inline, so a load called directly applies, or declines to, before it returns.
func TestALoadFinishingAfterTheWidgetWasRepointedIsDropped(t *testing.T) {
	test.NewApp()
	picture := twoPixelPNG(t)
	getter := func(chat.ID) ([]byte, error) { return picture, nil }

	di := bareDefaultImage(chat.NewID())
	di.generation = 2 // the widget has been pointed at a second user since the first load started
	applied := 0
	di.newBackgroundCallback = func() { applied++ }

	firstUsersImage := chat.NewID()
	di.setFirstAvailableImage(1, []chat.ID{firstUsersImage}, di.size, getter)
	assert.Equal(t, nil, di.backgroundColor.Image)
	assert.True(t, di.foregroundText.Visible())
	assert.Equal(t, 0, applied)

	// the first user's picture is still cached for the next time they are shown
	imageCacheMutex.Lock()
	_, cached := imageCache[firstUsersImage.String()+"32"]
	imageCacheMutex.Unlock()
	assert.True(t, cached)

	di.setFirstAvailableImage(2, []chat.ID{chat.NewID()}, di.size, getter)
	assert.NotEqual(t, nil, di.backgroundColor.Image)
	assert.False(t, di.foregroundText.Visible())
	assert.Equal(t, 1, applied)
}

func TestALoadFallsBackToOlderImagesInOrder(t *testing.T) {
	test.NewApp()
	picture := twoPixelPNG(t)
	broken, older, newest := chat.NewID(), chat.NewID(), chat.NewID()
	var asked []chat.ID
	getter := func(id chat.ID) ([]byte, error) {
		asked = append(asked, id)
		if id == older {
			return picture, nil
		}
		return []byte("not an image"), nil
	}

	di := bareDefaultImage(chat.NewID())
	di.generation = 1
	di.setFirstAvailableImage(1, []chat.ID{broken, older, newest}, di.size, getter)
	assert.Equal(t, []chat.ID{newest, older}, asked)
	assert.NotEqual(t, nil, di.backgroundColor.Image)
}

func TestEachSetBackgroundStartsANewGeneration(t *testing.T) {
	test.NewApp()
	di := bareDefaultImage(chat.NewID())
	di.setBackground()
	di.setBackground()
	assert.Equal(t, uint64(2), di.generation)
}
