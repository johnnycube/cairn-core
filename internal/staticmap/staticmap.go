// Package staticmap renders a static raster snapshot of an activity's course —
// the GPS polyline composited over slippy-map basemap tiles — as a PNG. It is
// used for activity feeds, list thumbnails, and (especially) federation, where
// a stable image URL is needed instead of an interactive map.
//
// The renderer is stdlib-only (image/draw + image/png) and takes the basemap
// TileFetcher as a parameter, so it needs no external map API key and is fully
// unit-testable without network access. When fetch is nil (or a tile errors),
// it degrades to drawing the route on a plain background.
//
// To look identical to the interactive MapLibre view rather than a coarse
// thumbnail, the renderer supersamples: it draws everything at `Scale`× the
// output size (fetching retina @2x tiles), then box-downscales to the target.
// That yields crisp basemap tiles and an anti-aliased route, matching the
// orange-on-dark-casing stroke and the start/finish markers of the live map.
package staticmap

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
)

const tileSize = 256 // logical (un-scaled) slippy tile size in world pixels

// LatLng is one GPS coordinate.
type LatLng struct{ Lat, Lng float64 }

// TileFetcher returns the basemap tile image at slippy coords (z, x, y). When
// Scale > 1 the renderer expects retina (@2x) tiles, i.e. tileSize*Scale px
// square; smaller tiles are scaled up to fit (and will look soft).
type TileFetcher func(ctx context.Context, z, x, y int) (image.Image, error)

// Options controls the rendered image.
type Options struct {
	Width, Height int         // output pixel size (after downscale)
	Padding       int         // min px between the route bbox and the edges
	LineColor     color.Color // route stroke
	LineWidth     int         // route stroke width in px
	CasingColor   color.Color // dark outline drawn under the route (nil = none)
	CasingWidth   int         // casing stroke width in px (total, incl. line)
	StartColor    color.Color // start marker fill (nil = no markers)
	FinishColor   color.Color // finish marker fill
	MarkerRadius  int         // endpoint marker radius in px
	Background    color.Color // fill when there is no basemap tile
	MaxZoom       int         // cap (slippy z); 16 is a sensible default
	Scale         int         // supersample factor (1 = none, 2 = retina). Default 2.
}

// DefaultOptions returns feed-thumbnail defaults that mirror the interactive map:
// an orange route over a dark casing, with green start / red finish markers.
func DefaultOptions() Options {
	return Options{
		// 3:1 short banner — matches the feed/overview thumbnail boxes so the
		// course fills a wide, low card without object-cover cropping. The
		// route reads small at this height, which is the intended trade-off.
		Width: 720, Height: 240, Padding: 20,
		LineColor:    color.RGBA{R: 0xec, G: 0x7a, B: 0x45, A: 0xff}, // matches MapLibre route-line
		LineWidth:    4,
		CasingColor:  color.RGBA{R: 0x0a, G: 0x0a, B: 0x0a, A: 0xc0}, // matches route-casing
		CasingWidth:  8,
		StartColor:   color.RGBA{R: 0x22, G: 0xc5, B: 0x5e, A: 0xff}, // green-500
		FinishColor:  color.RGBA{R: 0xef, G: 0x44, B: 0x44, A: 0xff}, // red-500
		MarkerRadius: 6,
		Background:   color.RGBA{R: 0x18, G: 0x18, B: 0x1b, A: 0xff}, // zinc-900
		MaxZoom:      16,
		Scale:        2,
	}
}

// RenderPNG renders the course and writes it as PNG to w.
func RenderPNG(ctx context.Context, out io.Writer, points []LatLng, opts Options, fetch TileFetcher) error {
	img, err := Render(ctx, points, opts, fetch)
	if err != nil {
		return err
	}
	return png.Encode(out, img)
}

// Render composites the basemap + route into an RGBA image at Options.Width ×
// Options.Height. Internally it draws at Scale× that size and box-downscales.
func Render(ctx context.Context, points []LatLng, opts Options, fetch TileFetcher) (*image.RGBA, error) {
	if opts.Width <= 0 {
		opts.Width = 600
	}
	if opts.Height <= 0 {
		opts.Height = 320
	}
	if opts.LineWidth <= 0 {
		opts.LineWidth = 3
	}
	if opts.MaxZoom <= 0 {
		opts.MaxZoom = 16
	}
	if opts.Scale <= 0 {
		opts.Scale = 2
	}
	s := opts.Scale

	pts := finitePoints(points)
	// Supersampled canvas.
	canvas := image.NewRGBA(image.Rect(0, 0, opts.Width*s, opts.Height*s))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: opts.Background}, image.Point{}, draw.Src)
	if len(pts) == 0 {
		return finish(canvas, s), nil
	}

	z := chooseZoom(pts, opts)
	// Canvas world-pixel origin (logical px): centre the route bbox.
	minX, minY, maxX, maxY := worldBBox(pts, z)
	centerX, centerY := (minX+maxX)/2, (minY+maxY)/2
	originX := centerX - float64(opts.Width)/2
	originY := centerY - float64(opts.Height)/2

	if fetch != nil {
		drawTiles(ctx, canvas, z, originX, originY, opts, s, fetch)
	}
	drawRoute(canvas, pts, z, originX, originY, opts, s)
	drawEndpoints(canvas, pts, z, originX, originY, opts, s)
	return finish(canvas, s), nil
}

// finish box-downscales the supersampled canvas to the output size (no-op at s=1).
func finish(canvas *image.RGBA, s int) *image.RGBA {
	if s <= 1 {
		return canvas
	}
	return boxDownscale(canvas, s)
}

func finitePoints(in []LatLng) []LatLng {
	out := make([]LatLng, 0, len(in))
	for _, p := range in {
		if isFinite(p.Lat) && isFinite(p.Lng) && p.Lat >= -85 && p.Lat <= 85 {
			out = append(out, p)
		}
	}
	return out
}

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// project converts lat/lng to absolute world pixels at zoom z (Web Mercator).
func project(lat, lng float64, z int) (float64, float64) {
	n := math.Exp2(float64(z))
	x := (lng + 180) / 360 * n * tileSize
	latRad := lat * math.Pi / 180
	y := (1 - math.Log(math.Tan(latRad)+1/math.Cos(latRad))/math.Pi) / 2 * n * tileSize
	return x, y
}

func worldBBox(pts []LatLng, z int) (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		x, y := project(p.Lat, p.Lng, z)
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	return
}

// chooseZoom picks the largest zoom at which the route bbox fits the padded
// canvas (so the route fills as much of the frame as possible).
func chooseZoom(pts []LatLng, opts Options) int {
	availW := float64(opts.Width - 2*opts.Padding)
	availH := float64(opts.Height - 2*opts.Padding)
	if availW < 1 {
		availW = 1
	}
	if availH < 1 {
		availH = 1
	}
	for z := opts.MaxZoom; z >= 0; z-- {
		minX, minY, maxX, maxY := worldBBox(pts, z)
		if (maxX-minX) <= availW && (maxY-minY) <= availH {
			return z
		}
	}
	return 0
}

func drawTiles(ctx context.Context, canvas *image.RGBA, z int, originX, originY float64, opts Options, s int, fetch TileFetcher) {
	maxIdx := int(math.Exp2(float64(z))) - 1
	firstTX := int(math.Floor(originX / tileSize))
	firstTY := int(math.Floor(originY / tileSize))
	lastTX := int(math.Floor((originX + float64(opts.Width)) / tileSize))
	lastTY := int(math.Floor((originY + float64(opts.Height)) / tileSize))
	dstTile := tileSize * s
	for tx := firstTX; tx <= lastTX; tx++ {
		for ty := firstTY; ty <= lastTY; ty++ {
			if tx < 0 || ty < 0 || tx > maxIdx || ty > maxIdx {
				continue
			}
			tile, err := fetch(ctx, z, tx, ty)
			if err != nil || tile == nil {
				continue // graceful: leave the background showing
			}
			dx := int(math.Round((float64(tx*tileSize) - originX) * float64(s)))
			dy := int(math.Round((float64(ty*tileSize) - originY) * float64(s)))
			dst := image.Rect(dx, dy, dx+dstTile, dy+dstTile)
			if tile.Bounds().Dx() == dstTile && tile.Bounds().Dy() == dstTile {
				draw.Draw(canvas, dst, tile, tile.Bounds().Min, draw.Over)
			} else {
				drawTileScaled(canvas, dst, tile)
			}
		}
	}
}

// drawTileScaled nearest-neighbour-scales src to fill dst (fallback path when a
// tile isn't already the expected retina size; the main path is a direct copy).
func drawTileScaled(dst *image.RGBA, r image.Rectangle, src image.Image) {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 {
		return
	}
	dw, dh := r.Dx(), r.Dy()
	for y := 0; y < dh; y++ {
		sy := sb.Min.Y + y*sh/dh
		for x := 0; x < dw; x++ {
			sx := sb.Min.X + x*sw/dw
			px, py := r.Min.X+x, r.Min.Y+y
			if (image.Point{px, py}).In(dst.Bounds()) {
				dst.Set(px, py, src.At(sx, sy))
			}
		}
	}
}

// drawRoute strokes the polyline: the dark casing first, then the colour on top
// (matching the MapLibre route-casing / route-line layer pair).
func drawRoute(canvas *image.RGBA, pts []LatLng, z int, originX, originY float64, opts Options, s int) {
	if opts.CasingColor != nil && opts.CasingWidth > opts.LineWidth {
		strokePath(canvas, pts, z, originX, originY, s, opts.CasingWidth*s, opts.CasingColor)
	}
	strokePath(canvas, pts, z, originX, originY, s, opts.LineWidth*s, opts.LineColor)
}

func strokePath(canvas *image.RGBA, pts []LatLng, z int, originX, originY float64, s, widthPx int, col color.Color) {
	radius := widthPx / 2
	if radius < 1 {
		radius = 1
	}
	toCanvas := func(p LatLng) (int, int) {
		x, y := project(p.Lat, p.Lng, z)
		return int(math.Round((x - originX) * float64(s))), int(math.Round((y - originY) * float64(s)))
	}
	prevX, prevY := toCanvas(pts[0])
	fillDisc(canvas, prevX, prevY, radius, col)
	for _, p := range pts[1:] {
		cx, cy := toCanvas(p)
		drawThickLine(canvas, prevX, prevY, cx, cy, radius, col)
		prevX, prevY = cx, cy
	}
}

// drawEndpoints marks the start and finish with filled discs ringed in white,
// the way the live map flags route ends.
func drawEndpoints(canvas *image.RGBA, pts []LatLng, z int, originX, originY float64, opts Options, s int) {
	if opts.StartColor == nil && opts.FinishColor == nil {
		return
	}
	r := opts.MarkerRadius
	if r <= 0 {
		r = 6
	}
	toCanvas := func(p LatLng) (int, int) {
		x, y := project(p.Lat, p.Lng, z)
		return int(math.Round((x - originX) * float64(s))), int(math.Round((y - originY) * float64(s)))
	}
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	marker := func(p LatLng, fill color.Color) {
		if fill == nil {
			return
		}
		cx, cy := toCanvas(p)
		fillDisc(canvas, cx, cy, (r+2)*s, white) // white ring
		fillDisc(canvas, cx, cy, r*s, fill)      // coloured centre
	}
	// Draw finish first so that on an out-and-back the start sits on top.
	marker(pts[len(pts)-1], opts.FinishColor)
	marker(pts[0], opts.StartColor)
}

// drawThickLine draws a line by stamping a filled disc of radius r along it.
func drawThickLine(img *image.RGBA, x0, y0, x1, y1, r int, col color.Color) {
	dx := abs(x1 - x0)
	dy := abs(y1 - y0)
	steps := dx
	if dy > steps {
		steps = dy
	}
	if steps == 0 {
		fillDisc(img, x0, y0, r, col)
		return
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		x := int(math.Round(float64(x0) + t*float64(x1-x0)))
		y := int(math.Round(float64(y0) + t*float64(y1-y0)))
		fillDisc(img, x, y, r, col)
	}
}

// fillDisc fills a disc of the given radius around (cx, cy), alpha-compositing
// the colour over what's there (so semi-transparent casings blend).
func fillDisc(img *image.RGBA, cx, cy, radius int, col color.Color) {
	if radius < 1 {
		radius = 1
	}
	rr := radius * radius
	for yy := -radius; yy <= radius; yy++ {
		for xx := -radius; xx <= radius; xx++ {
			if xx*xx+yy*yy > rr {
				continue
			}
			px, py := cx+xx, cy+yy
			if (image.Point{px, py}).In(img.Bounds()) {
				img.Set(px, py, blend(img.RGBAAt(px, py), col))
			}
		}
	}
}

// blend composites src over dst (straight-alpha, opaque dst).
func blend(dst color.RGBA, src color.Color) color.RGBA {
	sr, sg, sb, sa := src.RGBA() // 16-bit pre-multiplied
	if sa == 0xffff {
		return color.RGBA{uint8(sr >> 8), uint8(sg >> 8), uint8(sb >> 8), 0xff}
	}
	if sa == 0 {
		return dst
	}
	a := sa // 0..65535
	inv := 0xffff - a
	r := (sr*0xffff + uint32(dst.R)*257*inv) / 0xffff / 257
	g := (sg*0xffff + uint32(dst.G)*257*inv) / 0xffff / 257
	b := (sb*0xffff + uint32(dst.B)*257*inv) / 0xffff / 257
	return color.RGBA{uint8(r), uint8(g), uint8(b), 0xff}
}

// boxDownscale averages each s×s block — exact area resampling for an integer
// factor, which anti-aliases the route and sharpens retina tiles.
func boxDownscale(src *image.RGBA, s int) *image.RGBA {
	b := src.Bounds()
	ow, oh := b.Dx()/s, b.Dy()/s
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	n := uint32(s * s)
	for oy := 0; oy < oh; oy++ {
		for ox := 0; ox < ow; ox++ {
			var rr, gg, bb, aa uint32
			for dy := 0; dy < s; dy++ {
				for dx := 0; dx < s; dx++ {
					c := src.RGBAAt(b.Min.X+ox*s+dx, b.Min.Y+oy*s+dy)
					rr += uint32(c.R)
					gg += uint32(c.G)
					bb += uint32(c.B)
					aa += uint32(c.A)
				}
			}
			out.SetRGBA(ox, oy, color.RGBA{
				uint8(rr / n), uint8(gg / n), uint8(bb / n), uint8(aa / n),
			})
		}
	}
	return out
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}
