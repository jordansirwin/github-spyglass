// genicon renders the menu bar icon: an octocat-ish head in right-facing
// profile, peering through a spyglass pointed out to the upper right.
// Monochrome (black + alpha) so it works as a macOS template icon.
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

// head in right-facing profile
var (
	headCX, headCY, headR = 36.0, 48.0, 32.0
)

// ears (triangles): back ear and front ear of the profile view
var (
	earBack  = [3][2]float64{{12, 26}, {18, 1}, {34, 22}}
	earFront = [3][2]float64{{38, 24}, {48, 0}, {56, 26}}
)

// eye (cut out of the head, just behind the eyepiece)
var (
	eyeX, eyeY = 44.0, 40.0
	eyeR       = 5.0
)

// spyglass: a telescope growing out of the face at eye level, pointing
// up-and-out to the right, with the classic stepped draw-tube profile
var (
	tubeAX, tubeAY = 52.0, 36.0 // eyepiece end, inside the head silhouette
	tubeBX, tubeBY = 87.0, 20.0 // objective end
)

// tubeSteps are the telescoping segments: up to each t, the half-width.
// The head edge sits near t≈0.32, so the narrow eyepiece pokes out of the
// face before the first step up.
var tubeSteps = []struct{ t, w float64 }{
	{0.42, 4.4},
	{0.68, 7.2},
	{0.94, 9.8},
	{1.0, 11.6}, // objective rim
}

func inCircle(x, y, cx, cy, r float64) bool {
	return math.Hypot(x-cx, y-cy) <= r
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

// opaque decides whether a subpixel sample is part of the icon.
func opaque(x, y float64) bool {
	tubeD, tubeT := tubeAt(x, y)
	if w := tubeWidth(tubeT); w >= 0 && tubeD <= w {
		return true
	}

	if !inCircle(x, y, headCX, headCY, headR) &&
		!inTriangle(x, y, earBack) && !inTriangle(x, y, earFront) {
		return false
	}
	// eye cutout
	if inCircle(x, y, eyeX, eyeY, eyeR) {
		return false
	}
	return true
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
			hits := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					fx := float64(x) + (float64(sx)+0.5)/ss
					fy := float64(y) + (float64(sy)+0.5)/ss
					if opaque(fx, fy) {
						hits++
					}
				}
			}
			if hits == 0 {
				continue
			}
			a := uint8(255 * hits / (ss * ss))
			img.SetRGBA(x, y, color.RGBA{0, 0, 0, a})
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
