// SPDX-License-Identifier: MPL-2.0

package loopy

// When a routine runs: "daily 08:00", "weekdays 09:30", "every 2h" or
// "every 30m", in the computer's time zone.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// schedule is a parsed routine time.
type schedule struct {
	every    time.Duration // "every …"
	hour     int
	minute   int
	weekdays bool
}

func parseSchedule(when string) (schedule, error) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(when)))
	if len(fields) != 2 {
		return schedule{}, fmt.Errorf("say when as \"daily 08:00\", \"weekdays 09:30\", \"every 2h\" or \"every 30m\"")
	}
	switch fields[0] {
	case "every":
		d, err := time.ParseDuration(fields[1])
		if err != nil || d < 5*time.Minute {
			return schedule{}, fmt.Errorf("\"every\" takes a duration of 5m or more, such as 30m or 2h")
		}
		return schedule{every: d}, nil
	case "daily", "weekdays":
		at, err := time.Parse("15:04", fields[1])
		if err != nil {
			return schedule{}, fmt.Errorf("the time is HH:MM, such as 08:00")
		}
		return schedule{hour: at.Hour(), minute: at.Minute(), weekdays: fields[0] == "weekdays"}, nil
	}
	return schedule{}, fmt.Errorf("say when as \"daily 08:00\", \"weekdays 09:30\", \"every 2h\" or \"every 30m\"")
}

// ValidSchedule says why when cannot be a routine's time, nil when it can.
func ValidSchedule(when string) error {
	_, err := parseSchedule(when)
	return err
}

// Next is the routine's next run after last (or after now for a routine
// never run), zero when its time cannot be read.
func Next(when string, last, now time.Time) time.Time {
	s, err := parseSchedule(when)
	if err != nil {
		return time.Time{}
	}
	from := last
	if from.IsZero() {
		from = now
	}
	if s.every > 0 {
		if last.IsZero() {
			return now.Add(s.every)
		}
		return last.Add(s.every)
	}
	from = from.In(now.Location())
	next := time.Date(from.Year(), from.Month(), from.Day(), s.hour, s.minute, 0, 0, now.Location())
	for !next.After(from) || (s.weekdays && (next.Weekday() == time.Saturday || next.Weekday() == time.Sunday)) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// Describe says a schedule in words: "every day at 08:00".
func Describe(when string) string {
	s, err := parseSchedule(when)
	switch {
	case err != nil:
		return when
	case s.every > 0:
		return "every " + strings.TrimSuffix(strings.TrimSuffix(s.every.String(), "0s"), "0m")
	case s.weekdays:
		return fmt.Sprintf("weekdays at %02d:%02d", s.hour, s.minute)
	}
	return "every day at " + strconv.Itoa(s.hour/10) + strconv.Itoa(s.hour%10) + ":" + fmt.Sprintf("%02d", s.minute)
}
