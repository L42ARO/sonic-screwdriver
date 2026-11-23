// Package web holds the job viewer's HTML templates.
package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"
)

//go:embed templates/*.html
var files embed.FS

var pages = template.Must(template.New("").Funcs(template.FuncMap{
	"ago":    ago,
	"money":  func(v float64) string { return fmt.Sprintf("$%.4f", v) },
	"pretty": pretty,
	"screen": screen,
}).ParseFS(files, "templates/*.html"))

// screen turns a stored screen geometry ({"width","height","scale"}) into "1280×720 (scale 1.5)".
func screen(raw string) string {
	var g struct {
		Width, Height int
		Scale         float64
	}
	if json.Unmarshal([]byte(raw), &g) != nil || g.Width == 0 {
		return raw
	}
	if g.Scale == 0 || g.Scale == 1 {
		return fmt.Sprintf("%d×%d", g.Width, g.Height)
	}
	return fmt.Sprintf("%d×%d (scale %g)", g.Width, g.Height, g.Scale)
}

// pretty indents a JSON document for display; anything else is shown as-is.
func pretty(raw json.RawMessage) string {
	var out bytes.Buffer
	if json.Indent(&out, raw, "", "  ") != nil {
		return string(raw)
	}
	return out.String()
}

// Render writes one page.
func Render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("web: %s: %v", name, err)
	}
}

// ago turns a stored timestamp into "12s ago", "4m ago" or a date.
func ago(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return t.Local().Format("Jan 2 15:04")
}
