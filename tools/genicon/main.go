// genicon renders the menu bar icon: a white octocat-ish head in
// right-facing profile peering through a big fuchsia spyglass held to its
// left eye (covering the right), pointed out to the upper right. The white
// body gets a thin dark outline so it stays visible on light menu bars.
//
// Usage: go run ./tools/genicon [output.png]
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const size = 88 // rendered size; macOS scales it down to menu bar height

// head in right-facing profile, fully on canvas
var (
	headCX, headCY, headR = 33.0, 48.0, 32.0
)

// ears (triangles): back ear and front ear of the profile view
var (
	earBack  = [3][2]float64{{9, 26}, {15, 1}, {31, 22}}
	earFront = [3][2]float64{{35, 24}, {45, 0}, {53, 26}}
)

// spyglass: a telescope starting well back on the face at the left eye,
// covering the right eye on its way out, pointing up-and-out to the right
var (
	tubeAX, tubeAY = 36.0, 40.0 // eyepiece end, at the left eye
	tubeBX, tubeBY = 84.0, 18.0 // objective end
)

// tubeSteps are the telescoping segments: up to each t, the half-width.
var tubeSteps = []struct{ t, w float64 }{
	{0.14, 4.6}, // eyepiece, visible against the white face
	{0.4, 7.8},
	{0.68, 10.8},
	{0.94, 13.2},
	{1.0, 15.0}, // objective rim
}

const outlineW = 2.0 // dark outline around the white body

var (
	bodyColor    = [3]float64{255, 255, 255} // white octocat
	outlineColor = [3]float64{70, 70, 78}    // dark gray outline
	glassColor   = [3]float64{255, 20, 147}  // fuchsia pink spyglass
)

func inCircle(x, y, cx, cy, r float64) bool {
	return math.Hypot(x-cx, y-cy) <= r
}

func segDist(px, py, ax, ay, bx, by float64) float64 {
	abx, aby := bx-ax, by-ay
	t := ((px-ax)*abx + (py-ay)*aby) / (abx*abx + aby*aby)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(ax+t*abx), py-(ay+t*aby))
}

func inTriangle(x, y float64, t [3][2]float64) bool {
	sign := func(ax, ay, bx, by, cx, cy float64) float64 {
		return (ax-cx)*(by-cy) - (bx-cx)*(ay-cy)
	}
	d1 := sign(x, y, t[0][0], t[0][1], t[1][0], t[1][1])
	d2 := sign(x, y, t[1][0], t[1][1], t[2][0], t[2][1])
	d3 := sign(x, y, t[2][0], t[2][1], t[0][0], t[0][1])
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

// nearTriangle reports whether the point is inside the triangle or within d
// of one of its edges.
func nearTriangle(x, y float64, t [3][2]float64, d float64) bool {
	if inTriangle(x, y, t) {
		return true
	}
	for i := range t {
		j := (i + 1) % 3
		if segDist(x, y, t[i][0], t[i][1], t[j][0], t[j][1]) <= d {
			return true
		}
	}
	return false
}

// tubeAt returns the distance from a point to the tube axis and the
// position along it (0 at the eyepiece, 1 at the objective, unclamped).
func tubeAt(x, y float64) (dist, t float64) {
	abx, aby := tubeBX-tubeAX, tubeBY-tubeAY
	t = ((x-tubeAX)*abx + (y-tubeAY)*aby) / (abx*abx + aby*aby)
	tc := math.Max(0, math.Min(1, t))
	dist = math.Hypot(x-(tubeAX+tc*abx), y-(tubeAY+tc*aby))
	return dist, t
}

// tubeWidth returns the tube half-width at position t, or -1 outside it.
func tubeWidth(t float64) float64 {
	if t < 0 || t > 1 {
		return -1
	}
	for _, s := range tubeSteps {
		if t <= s.t {
			return s.w
		}
	}
	return -1
}

const (
	clear = iota
	outline
	body
	glass
)

// classify decides what a subpixel sample is part of.
func classify(x, y float64) int {
	tubeD, tubeT := tubeAt(x, y)
	if w := tubeWidth(tubeT); w >= 0 && tubeD <= w {
		return glass
	}
	if inCircle(x, y, headCX, headCY, headR) ||
		inTriangle(x, y, earBack) || inTriangle(x, y, earFront) {
		return body
	}
	if inCircle(x, y, headCX, headCY, headR+outlineW) ||
		nearTriangle(x, y, earBack, outlineW) || nearTriangle(x, y, earFront, outlineW) {
		return outline
	}
	return clear
}

func main() {
	out := "cmd/github-spyglass-app/icon.png"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	const ss = 4 // supersampling per axis
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					fx := float64(x) + (float64(sx)+0.5)/ss
					fy := float64(y) + (float64(sy)+0.5)/ss
					var c [3]float64
					switch classify(fx, fy) {
					case glass:
						c = glassColor
					case body:
						c = bodyColor
					case outline:
						c = outlineColor
					default:
						continue
					}
					r += c[0]
					g += c[1]
					b += c[2]
					a += 255
				}
			}
			if a == 0 {
				continue
			}
			n := float64(ss * ss)
			// premultiplied alpha
			img.SetRGBA(x, y, color.RGBA{
				uint8(r / n), uint8(g / n), uint8(b / n), uint8(a / n),
			})
		}
	}

	f, err := os.Create(out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
