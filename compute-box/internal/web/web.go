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
}).ParseFS(files, "templates/*.html"))

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
