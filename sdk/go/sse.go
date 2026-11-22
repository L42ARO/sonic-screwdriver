package sonic

import (
	"bufio"
	"encoding/json"
	"io"
	"iter"
	"strings"
)

// events reads a Server-Sent Events stream as (event name, JSON data) pairs.
func events(body io.Reader) iter.Seq2[string, json.RawMessage] {
	return func(yield func(string, json.RawMessage) bool) {
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
		name, data := "message", []string{}
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(line[6:])
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimLeft(line[5:], " "))
			case line == "" && len(data) > 0:
				if !yield(name, json.RawMessage(strings.Join(data, "\n"))) {
					return
				}
				name, data = "message", data[:0]
			}
		}
	}
}
