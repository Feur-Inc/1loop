// SPDX-License-Identifier: MPL-2.0

package loopy

// When a routine runs: "daily 08:00", "weekdays 09:30", "mon,thu 18:00",
// "monthly 1 09:00", "once 2026-12-24 18:00", "every 2h" or "every 30m",
// in the time zone of where Loopy lives.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type schedule struct {
	every        time.Duration
	hour, minute int
	days         map[time.Weekday]bool // nil for all
	monthDay     int
	once         time.Time
}

var weekdayNames = map[string]time.Weekday{"mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6, "sun": 0}

const scheduleHelp = "say when as \"daily 08:00\", \"weekdays 09:30\", \"mon,thu 18:00\", \"monthly 1 09:00\", \"once 2026-12-24 18:00\", \"every 2h\" or \"every 30m\""

func parseSchedule(when string, zone *time.Location) (schedule, error) {
	return parseEvery(when, zone, time.Minute)
}

func parseEvery(when string, zone *time.Location, least time.Duration) (schedule, error) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(when)))
	wrong := fmt.Errorf(scheduleHelp)
	if len(fields) < 2 {
		return schedule{}, wrong
	}
	if fields[0] == "every" && len(fields) == 2 {
		d, err := time.ParseDuration(fields[1])
		if err != nil || d < least {
			return schedule{}, fmt.Errorf("\"every\" takes a duration of %d minutes or more, such as 30m or 2h", int(least.Minutes()))
		}
		return schedule{every: d}, nil
	}
	at, err := time.Parse("15:04", fields[len(fields)-1])
	if err != nil {
		return schedule{}, fmt.Errorf("the time is HH:MM, such as 08:00; %s", scheduleHelp)
	}
	s := schedule{hour: at.Hour(), minute: at.Minute()}
	switch {
	case fields[0] == "daily" && len(fields) == 2:
	case fields[0] == "weekdays" && len(fields) == 2:
		s.days = map[time.Weekday]bool{1: true, 2: true, 3: true, 4: true, 5: true}
	case fields[0] == "monthly" && len(fields) == 3:
		s.monthDay, err = strconv.Atoi(fields[1])
		if err != nil || s.monthDay < 1 || s.monthDay > 31 {
			return schedule{}, wrong
		}
	case fields[0] == "once" && len(fields) == 3:
		s.once, err = time.ParseInLocation("2006-01-02 15:04", fields[1]+" "+fields[2], zone)
		if err != nil {
			return schedule{}, wrong
		}
	case len(fields) == 2:
		s.days = map[time.Weekday]bool{}
		for _, name := range strings.Split(fields[0], ",") {
			day, ok := weekdayNames[name]
			if !ok {
				return schedule{}, wrong
			}
			s.days[day] = true
		}
	default:
		return schedule{}, wrong
	}
	return s, nil
}

// ValidSchedule says why when cannot be a routine's time, nil when it can.
func ValidSchedule(when string) error {
	_, err := parseEvery(when, time.Local, 5*time.Minute)
	return err
}

// CheckSchedule says what is wrong with when; least is how often at most.
func CheckSchedule(when string, least time.Duration) error {
	_, err := parseEvery(when, time.Local, least)
	return err
}

// Next is the routine's first run after last, zero when there is none.
func Next(when string, last time.Time) time.Time {
	s, err := parseSchedule(when, last.Location())
	if err != nil {
		return time.Time{}
	}
	if s.every > 0 {
		return last.Add(s.every)
	}
	if !s.once.IsZero() {
		if s.once.After(last) {
			return s.once
		}
		return time.Time{}
	}
	next := time.Date(last.Year(), last.Month(), last.Day(), s.hour, s.minute, 0, 0, last.Location())
	for range 800 {
		end := time.Date(next.Year(), next.Month()+1, 0, 0, 0, 0, 0, next.Location()).Day()
		if next.After(last) && (s.days == nil || s.days[next.Weekday()]) && (s.monthDay == 0 || next.Day() == min(s.monthDay, end)) {
			return next
		}
		next = time.Date(next.Year(), next.Month(), next.Day()+1, s.hour, s.minute, 0, 0, next.Location())
	}
	return time.Time{}
}

// Describe says a schedule in words: "every day at 08:00".
func Describe(when string) string {
	s, err := parseSchedule(when, time.Local)
	clock := fmt.Sprintf("%02d:%02d", s.hour, s.minute)
	switch {
	case err != nil:
		return when
	case s.every > 0:
		text := s.every.String()
		if strings.HasSuffix(text, "m0s") {
			text = strings.TrimSuffix(text, "0s")
		}
		if strings.HasSuffix(text, "h0m") {
			text = strings.TrimSuffix(text, "0m")
		}
		return "every " + text
	case !s.once.IsZero():
		return "once, " + s.once.Format("Mon 2 Jan 2006 at 15:04")
	case s.monthDay > 0:
		return fmt.Sprintf("day %d of each month at %s", s.monthDay, clock)
	case len(s.days) == 5 && !s.days[0] && !s.days[6]:
		return "weekdays at " + clock
	case s.days != nil:
		var names []string
		for _, name := range []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"} {
			if s.days[weekdayNames[name]] {
				names = append(names, strings.ToUpper(name[:1])+name[1:])
			}
		}
		return strings.Join(names, ", ") + " at " + clock
	}
	return "every day at " + clock
}
