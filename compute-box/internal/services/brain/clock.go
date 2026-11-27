package brain

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"time"
)

// screenClock tells the model how much time has passed and how long the screen has
// looked the same. Without it the model can't tell a program that is slowly loading
// from one that stopped and is waiting for input, and it keeps waiting.
type screenClock struct {
	start, lastAction, changed time.Time
	prev                       []uint8 // coarse grayscale grid of the last full screenshot
}

const (
	gridW, gridH = 160, 90
	// A grid cell counts as different past this brightness change, and the screen
	// counts as changed when more than this share of cells differ. A blinking caret or
	// a ticking clock in the taskbar stays below it.
	cellThreshold   = 12
	changedFraction = 0.002
)

func newScreenClock(now time.Time, firstPNG string) *screenClock {
	return &screenClock{start: now, lastAction: now, changed: now, prev: grid(firstPNG)}
}

// observe records a new full screenshot taken at now and returns the line for the model.
// pngBase64 is empty for results without a full screenshot (zoom, failed actions).
func (c *screenClock) observe(now time.Time, pngBase64 string) string {
	sinceAction := now.Sub(c.lastAction)
	c.lastAction = now
	line := fmt.Sprintf("[%s since the job started, %s since your previous action.", short(now.Sub(c.start)), short(sinceAction))
	if g := grid(pngBase64); g != nil {
		if c.prev != nil && !differs(c.prev, g) {
			line += fmt.Sprintf(" The screen has not changed for %s.]", short(now.Sub(c.changed)))
		} else {
			c.changed = now
			line += " The screen changed.]"
		}
		c.prev = g
	} else {
		line += "]"
	}
	return line
}

func short(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm %02ds", s/60, s%60)
}

// grid samples the screenshot into a small grayscale grid, or nil if it can't be read.
func grid(pngBase64 string) []uint8 {
	if pngBase64 == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(pngBase64)
	if err != nil {
		return nil
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	b := img.Bounds()
	if b.Dx() < gridW || b.Dy() < gridH {
		return nil
	}
	out := make([]uint8, gridW*gridH)
	for gy := 0; gy < gridH; gy++ {
		for gx := 0; gx < gridW; gx++ {
			out[gy*gridW+gx] = gray(img, b.Min.X+gx*b.Dx()/gridW+b.Dx()/gridW/2, b.Min.Y+gy*b.Dy()/gridH+b.Dy()/gridH/2)
		}
	}
	return out
}

func gray(img image.Image, x, y int) uint8 {
	r, g, b, _ := img.At(x, y).RGBA()
	return uint8((299*r + 587*g + 114*b) / 1000 >> 8)
}

func differs(a, b []uint8) bool {
	if len(a) != len(b) {
		return true
	}
	n := 0
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d > cellThreshold || d < -cellThreshold {
			n++
		}
	}
	return float64(n) > changedFraction*float64(len(a))
}
