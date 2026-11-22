// Package notify sends push alerts through ntfy.sh.
package notify

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// Priorities as ntfy defines them.
const (
	Medium = "3"
	High   = "4"
)

type Notifier struct {
	topic  string
	client *http.Client
}

// New returns a notifier for an ntfy.sh topic. An empty topic disables alerts.
func New(topic string) *Notifier {
	return &Notifier{topic: topic, client: &http.Client{Timeout: 10 * time.Second}}
}

// Send posts an alert. Failures are logged, never returned: alerts must not break jobs.
func (n *Notifier) Send(priority, title, message string) {
	if n == nil || n.topic == "" {
		return
	}
	req, err := http.NewRequest(http.MethodPost, "https://ntfy.sh/"+n.topic, strings.NewReader(message))
	if err != nil {
		log.Printf("notify: %v", err)
		return
	}
	req.Header.Set("Title", title)
	req.Header.Set("Priority", priority)
	resp, err := n.client.Do(req)
	if err != nil {
		log.Printf("notify: %v", err)
		return
	}
	resp.Body.Close()
}
