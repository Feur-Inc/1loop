// SPDX-License-Identifier: MPL-2.0

package memory

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Span is a stretch of time, its end not included.
type Span struct{ From, To time.Time }

// Holds says whether at is in the span.
func (span Span) Holds(at time.Time) bool { return !at.Before(span.From) && at.Before(span.To) }

var (
	agoPattern   = regexp.MustCompile(`\b(?:il y a|ca fait)\s+(\w+)\s+(jours?|semaines?|mois|ans?|annees?)\b|\b(\w+)\s+(days?|weeks?|months?|years?)\s+ago\b`)
	datePattern  = regexp.MustCompile(`\b(20\d\d)-(\d\d)-(\d\d)\b`)
	dayPattern   = regexp.MustCompile(`\b(\d{1,2})(?:er|st|nd|rd|th)?\s+(?:of\s+)?([a-z]+)(?:\s+(20\d\d))?\b`)
	monthDay     = regexp.MustCompile(`\b([a-z]+)\s+(\d{1,2})(?:st|nd|rd|th)?(?:\s*,?\s+(20\d\d))?\b`)
	monthPattern = regexp.MustCompile(`\b(?:en|in|de|of|during|depuis|since)\s+([a-z]+)(?:\s+(20\d\d))?\b`)
	yearPattern  = regexp.MustCompile(`\b(?:en|in|during)\s+(20\d\d)\b`)
	weekPattern  = regexp.MustCompile(`\b(last|this)\s+([a-z]+)\b|\b([a-z]+)\s+(?:dernier|derniere|passe|passee)\b|\bcette\s+(semaine|annee)\b|\bce\s+(mois)\b`)
)

var monthNames = map[string]time.Month{"janvier": 1, "january": 1, "jan": 1, "fevrier": 2, "february": 2, "feb": 2, "mars": 3, "march": 3, "avril": 4, "april": 4, "mai": 5, "may": 5, "juin": 6, "june": 6, "juillet": 7, "july": 7, "aout": 8, "august": 8, "aug": 8, "septembre": 9, "september": 9, "sept": 9, "octobre": 10, "october": 10, "oct": 10, "novembre": 11, "november": 11, "nov": 11, "decembre": 12, "december": 12, "dec": 12}

var dayNames = map[string]time.Weekday{"lundi": 1, "monday": 1, "mardi": 2, "tuesday": 2, "mercredi": 3, "wednesday": 3, "jeudi": 4, "thursday": 4, "vendredi": 5, "friday": 5, "samedi": 6, "saturday": 6, "dimanche": 0, "sunday": 0}

var numberNames = map[string]int{"un": 1, "une": 1, "a": 1, "an": 1, "one": 1, "deux": 2, "two": 2, "trois": 3, "three": 3, "quatre": 4, "four": 4, "cinq": 5, "five": 5, "six": 6, "sept": 7, "seven": 7, "huit": 8, "eight": 8, "neuf": 9, "nine": 9, "dix": 10, "ten": 10, "quelques": 4, "few": 4}

// Moment is the past a text speaks of ("hier", "last week", "il y a trois
// mois", "in March", "12 mai"), in French and English; false when it names
// none.
func Moment(text string, at time.Time) (Span, bool) {
	s := strings.Join(strings.Fields(strings.NewReplacer("’", "'", ",", " , ").Replace(plain(text))), " ")
	today := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
	day := func(from time.Time) (Span, bool) { return Span{from, from.AddDate(0, 0, 1)}, true }
	has := func(words ...string) bool {
		for _, word := range words {
			if i := strings.Index(" "+s+" ", " "+word+" "); i >= 0 {
				return true
			}
		}
		return false
	}
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	month := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, at.Location())
	year := time.Date(at.Year(), 1, 1, 0, 0, 0, 0, at.Location())
	if m := datePattern.FindStringSubmatch(s); m != nil {
		if d, err := time.ParseInLocation("2006-01-02", m[0], at.Location()); err == nil {
			return day(d)
		}
	}
	if m := agoPattern.FindStringSubmatch(s); m != nil {
		count, unit := m[1]+m[3], m[2]+m[4]
		n, err := strconv.Atoi(count)
		if err != nil {
			n = numberNames[count]
		}
		if n > 0 {
			switch unit[0] {
			case 'j', 'd':
				return day(today.AddDate(0, 0, -n))
			case 's', 'w':
				from := monday.AddDate(0, 0, -7*n)
				return Span{from, from.AddDate(0, 0, 7)}, true
			case 'm':
				from := month.AddDate(0, -n, 0)
				return Span{from, from.AddDate(0, 1, 0)}, true
			case 'a', 'y':
				from := year.AddDate(-n, 0, 0)
				return Span{from, from.AddDate(1, 0, 0)}, true
			}
		}
	}
	switch {
	case has("avant-hier", "avant hier"):
		return day(today.AddDate(0, 0, -2))
	case has("hier", "yesterday", "last night"):
		return day(today.AddDate(0, 0, -1))
	case has("aujourd'hui", "today", "ce matin", "this morning", "tout a l'heure"):
		return day(today)
	}
	named := dayPattern.FindAllStringSubmatch(s, -1)
	for _, m := range monthDay.FindAllStringSubmatch(s, -1) {
		named = append(named, []string{m[0], m[2], m[1], m[3]})
	}
	for _, m := range named {
		number, name, yearText := m[1], m[2], m[3]
		if mon, ok := monthNames[name]; ok {
			n, _ := strconv.Atoi(number)
			y := at.Year()
			if yearText != "" {
				y, _ = strconv.Atoi(yearText)
			}
			d := time.Date(y, mon, n, 0, 0, 0, 0, at.Location())
			if n < 1 || n > 31 || d.Month() != mon {
				continue
			}
			if yearText == "" && d.After(today) {
				d = d.AddDate(-1, 0, 0)
			}
			return day(d)
		}
	}
	if m := yearPattern.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[1])
		from := time.Date(y, 1, 1, 0, 0, 0, 0, at.Location())
		return Span{from, from.AddDate(1, 0, 0)}, true
	}
	for _, m := range weekPattern.FindAllStringSubmatch(s, -1) {
		unit, past := m[2]+m[3]+m[4]+m[5], m[1] == "last" || m[3] != ""
		back := 0
		if past {
			back = 1
		}
		switch unit {
		case "week", "semaine":
			from := monday.AddDate(0, 0, -7*back)
			return Span{from, from.AddDate(0, 0, 7)}, true
		case "month", "mois":
			from := month.AddDate(0, -back, 0)
			return Span{from, from.AddDate(0, 1, 0)}, true
		case "year", "annee", "an":
			from := year.AddDate(-back, 0, 0)
			return Span{from, from.AddDate(1, 0, 0)}, true
		}
		if weekday, ok := dayNames[unit]; ok && past {
			d := today.AddDate(0, 0, -1)
			for d.Weekday() != weekday {
				d = d.AddDate(0, 0, -1)
			}
			return day(d)
		}
	}
	for _, m := range monthPattern.FindAllStringSubmatch(s, -1) {
		if mon, ok := monthNames[m[1]]; ok {
			y := at.Year()
			if m[2] != "" {
				y, _ = strconv.Atoi(m[2])
			} else if mon > at.Month() {
				y--
			}
			from := time.Date(y, mon, 1, 0, 0, 0, 0, at.Location())
			return Span{from, from.AddDate(0, 1, 0)}, true
		}
	}
	return Span{}, false
}
