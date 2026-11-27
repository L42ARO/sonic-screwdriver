package brain

import (
	"fmt"
	"time"
)

// jobClock tells the model how much time has passed. Screenshots alone don't show it,
// so without this the model can't tell how long a loading screen has been up, and it
// can't tell a slow program from one that stopped and is waiting for input.
type jobClock struct {
	start, lastAction time.Time
}

func newJobClock(now time.Time) *jobClock {
	return &jobClock{start: now, lastAction: now}
}

// observe returns the timing line for an action result received at now.
func (c *jobClock) observe(now time.Time) string {
	since := now.Sub(c.lastAction)
	c.lastAction = now
	return fmt.Sprintf("[%s since the job started, %s since your previous action.]", short(now.Sub(c.start)), short(since))
}

func short(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm %02ds", s/60, s%60)
}
