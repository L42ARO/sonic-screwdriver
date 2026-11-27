package brain

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"
)

// screen renders a 1280x720 screenshot: a gray background with a dark box at (x, 300),
// plus an optional 2-pixel "caret" that blinks.
func screen(t *testing.T, boxX int, caret bool) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for y := 0; y < 720; y++ {
		for x := 0; x < 1280; x++ {
			c := color.RGBA{192, 192, 192, 255}
			if x >= boxX && x < boxX+300 && y >= 300 && y < 400 {
				c = color.RGBA{0, 0, 128, 255}
			}
			if caret && x >= 640 && x < 642 && y >= 500 && y < 516 {
				c = color.RGBA{0, 0, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestScreenClockReportsTimeAndStillScreens(t *testing.T) {
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	splash := screen(t, 100, false)
	c := newScreenClock(start, splash)

	line := c.observe(start.Add(12*time.Second), splash)
	if !strings.Contains(line, "12s since the job started") || !strings.Contains(line, "not changed for 12s") {
		t.Fatalf("same screen: %q", line)
	}
	// A blinking caret doesn't count as the screen changing.
	line = c.observe(start.Add(25*time.Second), screen(t, 100, true))
	if !strings.Contains(line, "13s since your previous action") || !strings.Contains(line, "not changed for 25s") {
		t.Fatalf("caret blink: %q", line)
	}
	// Real progress resets the still-screen timer.
	line = c.observe(start.Add(70*time.Second), screen(t, 600, false))
	if !strings.Contains(line, "1m 10s since the job started") || !strings.Contains(line, "The screen changed.") {
		t.Fatalf("changed screen: %q", line)
	}
	// Results without a full screenshot (zoom, errors) report time only.
	line = c.observe(start.Add(75*time.Second), "")
	if strings.Contains(line, "screen") {
		t.Fatalf("no screenshot: %q", line)
	}
	line = c.observe(start.Add(80*time.Second), screen(t, 600, false))
	if !strings.Contains(line, "not changed for 10s") {
		t.Fatalf("still since last change: %q", line)
	}
}

func TestRulesExplainTheTimingAndTheStuckFallback(t *testing.T) {
	for _, want := range []string{"how long the screen has stayed the same", "title bar", "list_running_applications"} {
		if !strings.Contains(rules, want) {
			t.Errorf("rules don't mention %q", want)
		}
	}
}
