package staticmap

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

func solidTile(c color.Color) TileFetcher {
	return func(_ context.Context, _, _, _ int) (image.Image, error) {
		t := image.NewRGBA(image.Rect(0, 0, tileSize, tileSize))
		for y := 0; y < tileSize; y++ {
			for x := 0; x < tileSize; x++ {
				t.Set(x, y, c)
			}
		}
		return t, nil
	}
}

func sampleRoute() []LatLng {
	// A short ~diagonal track near Darmstadt.
	return []LatLng{
		{49.8700, 8.6500}, {49.8720, 8.6520}, {49.8740, 8.6550},
		{49.8760, 8.6590}, {49.8780, 8.6620},
	}
}

func TestRender_SizeAndPNG(t *testing.T) {
	opts := DefaultOptions()
	opts.Width, opts.Height = 400, 240
	img, err := Render(context.Background(), sampleRoute(), opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 400 || img.Bounds().Dy() != 240 {
		t.Fatalf("size = %v, want 400x240", img.Bounds())
	}
	// Encodes to a decodable PNG.
	var buf bytes.Buffer
	if err := RenderPNG(context.Background(), &buf, sampleRoute(), opts, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("output is not valid PNG: %v", err)
	}
}

func TestRender_DrawsLineAndTiles(t *testing.T) {
	opts := DefaultOptions()
	opts.Width, opts.Height = 400, 240
	tileCol := color.RGBA{R: 0x33, G: 0x66, B: 0x99, A: 0xff}
	img, err := Render(context.Background(), sampleRoute(), opts, solidTile(tileCol))
	if err != nil {
		t.Fatal(err)
	}
	// The basemap tile colour should appear somewhere (composited).
	if !hasColor(img, tileCol) {
		t.Error("expected the basemap tile colour to be composited in")
	}
	// The route line colour should appear somewhere.
	if !hasColor(img, color.RGBA{R: 0xec, G: 0x7a, B: 0x45, A: 0xff}) {
		t.Error("expected the route line colour to be drawn")
	}
}

func TestRender_EmptyAndDegenerate(t *testing.T) {
	opts := DefaultOptions()
	// No points → background only, no panic.
	if _, err := Render(context.Background(), nil, opts, nil); err != nil {
		t.Fatal(err)
	}
	// A single point → still renders (centred, default zoom path).
	if _, err := Render(context.Background(), []LatLng{{49.87, 8.65}}, opts, nil); err != nil {
		t.Fatal(err)
	}
	// NaN/Inf coords are filtered out.
	bad := []LatLng{{Lat: 49.87, Lng: 8.65}, {Lat: math.Inf(1), Lng: 0}}
	if _, err := Render(context.Background(), bad, opts, nil); err != nil {
		t.Fatal(err)
	}
}

func TestChooseZoom_Monotonic(t *testing.T) {
	opts := DefaultOptions()
	tight := []LatLng{{49.8700, 8.6500}, {49.8702, 8.6502}} // ~25 m apart
	wide := []LatLng{{49.0, 8.0}, {50.0, 9.0}}              // ~100 km apart
	zTight := chooseZoom(tight, opts)
	zWide := chooseZoom(wide, opts)
	if zTight <= zWide {
		t.Errorf("a tighter route should zoom in further: tight=%d wide=%d", zTight, zWide)
	}
}

func hasColor(img *image.RGBA, c color.Color) bool {
	wr, wg, wb, _ := c.RGBA()
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, _ := img.At(x, y).RGBA()
			if r == wr && g == wg && bb == wb {
				return true
			}
		}
	}
	return false
}
