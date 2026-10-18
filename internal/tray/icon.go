package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/ahaltindis/wipsync/internal/watch"
)

// Colours of the icon, by state, for a dark bar.
var colours = map[watch.State]color.NRGBA{
	watch.UpToDate:     {0xd0, 0xd0, 0xd0, 0xff}, // light grey, like the bar's other icons
	watch.Ahead:        {0x17, 0x93, 0xd1, 0xff}, // blue
	watch.Behind:       {0xc6, 0x78, 0xdd, 0xff}, // purple
	watch.Snapshotting: {0xe5, 0xc0, 0x7b, 0xff}, // yellow
	watch.Offline:      {0xe0, 0x6c, 0x75, 0xff}, // red
}

const (
	size   = 64 // pixels; the tray scales it down
	sample = 4  // samples per pixel along each axis, for smooth edges
)

// icon draws the wipsync symbol, a W whose tips run on into two arrows
// round a circle, in c, as a PNG.
func icon(c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			hits := 0
			for sy := range sample {
				for sx := range sample {
					px := (float64(x) + (float64(sx)+0.5)/sample) / size
					py := (float64(y) + (float64(sy)+0.5)/sample) / size
					if inSymbol(px-0.5, py-0.5) {
						hits++
					}
				}
			}
			if hits > 0 {
				a := c
				a.A = uint8(int(c.A) * hits / (sample * sample))
				img.SetNRGBA(x, y, a)
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img) // writing to memory does not fail
	return b.Bytes()
}

// The symbol in a unit square centred on 0, 0 (y down). Angles are in
// degrees, clockwise from 3 o'clock. The W's tips sit on a circle of radius r
// at tip degrees above the middle; from the left one an arc runs round the
// bottom, from the right one an arc over the top, both anticlockwise, each
// ending in a head that points at the other tip. Strokes are w wide.
const (
	r   = 0.37
	w   = 0.08
	tip = 35.0
)

var (
	left, right = onCircle(180 + tip), onCircle(360 - tip)
	// the W: tip, bottom, middle, bottom, tip
	strokes = [][2][2]float64{
		{left, {left[0] / 2, 0.12}},
		{{left[0] / 2, 0.12}, {0, -0.07}},
		{{0, -0.07}, {right[0] / 2, 0.12}},
		{{right[0] / 2, 0.12}, right},
	}
	arcs = [][2]float64{{44, 180 + tip}, {270, 360 - tip}} // each arc's head is at its first angle
)

func inSymbol(x, y float64) bool {
	for _, s := range strokes {
		if nearSegment(x, y, s[0], s[1]) {
			return true
		}
	}
	d := math.Hypot(x, y)
	angle := math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
	for _, a := range arcs {
		if math.Abs(d-r) <= w/2 && angle >= a[0] && angle <= a[1] {
			return true
		}
		if inHead(x, y, a[0]*math.Pi/180) {
			return true
		}
	}
	return false
}

func onCircle(deg float64) [2]float64 {
	a := deg * math.Pi / 180
	return [2]float64{r * math.Cos(a), r * math.Sin(a)}
}

// nearSegment reports whether x, y is within w/2 of the segment from p to q.
func nearSegment(x, y float64, p, q [2]float64) bool {
	dx, dy := q[0]-p[0], q[1]-p[1]
	t := ((x-p[0])*dx + (y-p[1])*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(x-p[0]-t*dx, y-p[1]-t*dy) <= w/2
}

// inHead reports whether x, y is in the arrow head at the anticlockwise end
// of an arc, at angle a (radians).
func inHead(x, y, a float64) bool {
	px, py := r*math.Cos(a), r*math.Sin(a) // the arc's end
	nx, ny := math.Cos(a), math.Sin(a)     // outwards
	tx, ty := math.Sin(a), -math.Cos(a)    // along the arc, anticlockwise
	b1 := [2]float64{px + nx*w*1.45, py + ny*w*1.45}
	b2 := [2]float64{px - nx*w*1.45, py - ny*w*1.45}
	tip := [2]float64{px + tx*w*1.7, py + ty*w*1.7}
	return inTriangle([2]float64{x, y}, b1, b2, tip)
}

func inTriangle(p, a, b, c [2]float64) bool {
	side := func(p, q, r [2]float64) float64 {
		return (p[0]-r[0])*(q[1]-r[1]) - (q[0]-r[0])*(p[1]-r[1])
	}
	d1, d2, d3 := side(p, a, b), side(p, b, c), side(p, c, a)
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}
