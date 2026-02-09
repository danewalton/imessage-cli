// Package tui provides image rendering for terminal display using half-block characters.
package tui

import (
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// RenderImageToText renders an image file as a string of half-block characters
// with tview color tags. Each character cell encodes two vertical pixels using
// the upper-half-block character (▀) with foreground = top pixel, background =
// bottom pixel. This gives 2× vertical resolution in a standard terminal.
//
// maxWidth and maxHeight are in terminal cells. The image is scaled to fit
// within these bounds while preserving aspect ratio. maxHeight is in cell rows
// (each row = 2 pixels).
func RenderImageToText(filePath string, maxWidth, maxHeight int) (string, error) {
	// Handle HEIC/HEIF by converting via sips (macOS built-in)
	actualPath, cleanup, err := ensureDecodable(filePath)
	if err != nil {
		return "", fmt.Errorf("cannot prepare image: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	f, err := os.Open(actualPath)
	if err != nil {
		return "", fmt.Errorf("cannot open image: %w", err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("cannot decode image: %w", err)
	}

	// Scale image to fit within bounds
	bounds := img.Bounds()
	imgW := bounds.Dx()
	imgH := bounds.Dy()

	// maxHeight is in rows; each row = 2 pixels
	maxPixH := maxHeight * 2

	scaleX := float64(maxWidth) / float64(imgW)
	scaleY := float64(maxPixH) / float64(imgH)
	scale := math.Min(scaleX, scaleY)
	if scale > 1.0 {
		scale = 1.0 // don't upscale
	}

	targetW := int(float64(imgW) * scale)
	targetH := int(float64(imgH) * scale)
	if targetW < 1 {
		targetW = 1
	}
	if targetH < 1 {
		targetH = 1
	}
	// Make targetH even for clean half-block pairing
	if targetH%2 != 0 {
		targetH++
	}

	// Simple nearest-neighbor resize
	resized := resizeNearest(img, targetW, targetH)

	// Render using half-block characters with tview color tags
	var sb strings.Builder
	for y := 0; y < targetH; y += 2 {
		for x := 0; x < targetW; x++ {
			top := colorAt(resized, x, y)
			bot := colorAt(resized, x, y+1)

			tr, tg, tb := rgbComponents(top)
			br, bg, bb := rgbComponents(bot)

			// tview uses #RRGGBB hex color tags
			sb.WriteString(fmt.Sprintf("[#%02x%02x%02x:#%02x%02x%02x]▀[-:-]",
				tr, tg, tb, br, bg, bb))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// ensureDecodable converts HEIC/HEIF files to JPEG using macOS sips.
// Returns the path to use for decoding and an optional cleanup function.
func ensureDecodable(filePath string) (string, func(), error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".heic" && ext != ".heif" {
		return filePath, nil, nil
	}

	// Create temp file for conversion
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("imsg-preview-%d.jpg", os.Getpid()))

	// sips is available on all macOS systems
	cmd := exec.Command("sips", "-s", "format", "jpeg", filePath, "--out", tmpFile)
	if err := cmd.Run(); err != nil {
		return "", nil, fmt.Errorf("sips conversion failed: %w", err)
	}

	cleanup := func() {
		os.Remove(tmpFile)
	}
	return tmpFile, cleanup, nil
}

// resizeNearest performs nearest-neighbor image resize.
func resizeNearest(img image.Image, w, h int) image.Image {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		srcY := bounds.Min.Y + y*srcH/h
		for x := 0; x < w; x++ {
			srcX := bounds.Min.X + x*srcW/w
			dst.Set(x, y, img.At(srcX, srcY))
		}
	}
	return dst
}

// colorAt safely gets a color at (x) in row y, returning black if out of bounds.
func colorAt(img image.Image, x, y int) color.Color {
	bounds := img.Bounds()
	if y >= bounds.Max.Y || x >= bounds.Max.X {
		return color.Black
	}
	return img.At(x+bounds.Min.X, y+bounds.Min.Y)
}

// rgbComponents extracts 8-bit RGB from a color.
func rgbComponents(c color.Color) (uint8, uint8, uint8) {
	r, g, b, _ := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}
