package evidence

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Date is a calendar date found in source text.
type Date struct {
	ISO       string // YYYY-MM-DD; first of the month when Precision is "month"
	Precision string // "day" or "month"
	Start     int    // byte offsets of the match in the searched text
	End       int
}

// Time returns the date at midnight UTC.
func (d Date) Time() time.Time {
	t, _ := time.Parse("2006-01-02", d.ISO)
	return t
}

const monthAlt = `jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?`

var (
	reISO       = regexp.MustCompile(`\b(20\d{2})[-/](0?[1-9]|1[0-2])[-/](0?[1-9]|[12]\d|3[01])\b`)
	reMonthDay  = regexp.MustCompile(`(?i)\b(` + monthAlt + `)\.?\s+(\d{1,2})(?:st|nd|rd|th)?\s*,?\s+(20\d{2})\b`)
	reDayMonth  = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)?\s+(?:of\s+)?(` + monthAlt + `)\.?\s*,?\s+(20\d{2})\b`)
	reMonthYear = regexp.MustCompile(`(?i)\b(` + monthAlt + `)\.?\s*,?\s+(20\d{2})\b`)
)

var monthNum = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

func monthOf(s string) int {
	s = strings.ToLower(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return monthNum[s]
}

func validDate(y, m, d int) bool {
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t.Year() == y && int(t.Month()) == m && t.Day() == d
}

// FindDates returns every unambiguous calendar date in text, in order of
// appearance. Numeric day/month orders such as 01/02/2026 are skipped on
// purpose: the reader cannot know which field is the month, so Heimdall will
// not build a shutdown date on one.
func FindDates(text string) []Date {
	var out []Date
	covered := func(s, e int) bool {
		for _, d := range out {
			if s < d.End && e > d.Start {
				return true
			}
		}
		return false
	}
	add := func(y, m, d int, prec string, s, e int) {
		if !validDate(y, m, d) || covered(s, e) {
			return
		}
		out = append(out, Date{ISO: fmt.Sprintf("%04d-%02d-%02d", y, m, d), Precision: prec, Start: s, End: e})
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }

	for _, m := range reISO.FindAllStringSubmatchIndex(text, -1) {
		add(atoi(text[m[2]:m[3]]), atoi(text[m[4]:m[5]]), atoi(text[m[6]:m[7]]), "day", m[0], m[1])
	}
	for _, m := range reMonthDay.FindAllStringSubmatchIndex(text, -1) {
		add(atoi(text[m[6]:m[7]]), monthOf(text[m[2]:m[3]]), atoi(text[m[4]:m[5]]), "day", m[0], m[1])
	}
	for _, m := range reDayMonth.FindAllStringSubmatchIndex(text, -1) {
		add(atoi(text[m[6]:m[7]]), monthOf(text[m[4]:m[5]]), atoi(text[m[2]:m[3]]), "day", m[0], m[1])
	}
	for _, m := range reMonthYear.FindAllStringSubmatchIndex(text, -1) {
		add(atoi(text[m[4]:m[5]]), monthOf(text[m[2]:m[3]]), 1, "month", m[0], m[1])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// DateIn reports whether text contains the given ISO date, and with what
// precision. A month-precision mention matches any day of that month.
func DateIn(text, iso string) (Date, bool) {
	for _, d := range FindDates(text) {
		if d.ISO == iso {
			return d, true
		}
		if d.Precision == "month" && len(iso) >= 7 && d.ISO[:7] == iso[:7] {
			return d, true
		}
	}
	return Date{}, false
}

// NormalizeISO accepts a date an analyst model wrote in any supported form
// and returns it as YYYY-MM-DD, or "" when it is not a date.
func NormalizeISO(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "null") || strings.EqualFold(s, "none") || strings.EqualFold(s, "unknown") {
		return ""
	}
	if ds := FindDates(s); len(ds) > 0 {
		return ds[0].ISO
	}
	return ""
}

// DaysUntil returns whole days from now until the ISO date; negative when past.
func DaysUntil(iso string, now time.Time) (int, bool) {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return 0, false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return int(t.Sub(today).Hours() / 24), true
}
