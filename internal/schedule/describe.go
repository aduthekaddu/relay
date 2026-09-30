package schedule

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// minInterval is the shortest allowed "@every" interval.
const minInterval = time.Minute

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ParseCron parses a standard 5-field expression or a descriptor (@daily,
// @every 2h, …) in the given IANA timezone ("" = server local time).
func ParseCron(expr, tz string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("cron expression is required")
	}
	if strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=") {
		return nil, fmt.Errorf("set the timezone field instead of a TZ= prefix")
	}
	spec := expr
	if tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return nil, fmt.Errorf("unknown timezone %q", tz)
		}
		spec = "CRON_TZ=" + tz + " " + expr
	}
	sch, err := parser.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	if every, ok := sch.(cron.ConstantDelaySchedule); ok && every.Delay < minInterval {
		return nil, fmt.Errorf("@every intervals must be at least a minute")
	}
	return sch, nil
}

// NextRuns returns the next n activation times after from.
func NextRuns(expr, tz string, from time.Time, n int) ([]time.Time, error) {
	sch, err := ParseCron(expr, tz)
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, n)
	t := from
	for i := 0; i < n; i++ {
		t = sch.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t)
	}
	return out, nil
}

var (
	dayNames   = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	monthNames = []string{"", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	dowAlias   = map[string]string{"sun": "0", "mon": "1", "tue": "2", "wed": "3", "thu": "4", "fri": "5", "sat": "6"}
	monAlias   = map[string]string{"jan": "1", "feb": "2", "mar": "3", "apr": "4", "may": "5", "jun": "6", "jul": "7", "aug": "8", "sep": "9", "oct": "10", "nov": "11", "dec": "12"}
)

// Describe renders a cron expression in plain English, e.g.
// "0 2 * * 1-5" → "Every weekday at 02:00". Unusual expressions fall back
// to "Custom schedule (<expr>)".
func Describe(expr string) string {
	expr = strings.TrimSpace(expr)
	switch strings.ToLower(expr) {
	case "@hourly":
		return "Every hour"
	case "@daily", "@midnight":
		return "Every day at 00:00"
	case "@weekly":
		return "Every Sunday at 00:00"
	case "@monthly":
		return "Monthly on the 1st at 00:00"
	case "@yearly", "@annually":
		return "Every year on January 1 at 00:00"
	}
	if rest, ok := strings.CutPrefix(expr, "@every "); ok {
		d, err := time.ParseDuration(strings.TrimSpace(rest))
		if err != nil {
			return fallback(expr)
		}
		return "Every " + humanDuration(d)
	}
	f := strings.Fields(strings.ToLower(expr))
	if len(f) != 5 {
		return fallback(expr)
	}
	min, hour, dom, mon, dow := f[0], replaceNames(f[1], nil), f[2], replaceNames(f[3], monAlias), replaceNames(f[4], dowAlias)
	allDays := star(dom) && star(mon) && star(dow)

	switch {
	case star(min) && star(hour) && allDays:
		return "Every minute"
	case stepOf(min) > 0 && star(hour) && allDays:
		return "Every " + plural(stepOf(min), "minute")
	case isNum(min) && star(hour) && allDays:
		if atoi(min) == 0 {
			return "Every hour"
		}
		return fmt.Sprintf("Every hour at :%02d", atoi(min))
	case isNum(min) && stepOf(hour) > 0 && allDays:
		s := "Every " + plural(stepOf(hour), "hour")
		if m := atoi(min); m != 0 {
			s += fmt.Sprintf(" at :%02d", m)
		}
		return s
	}

	mins, ok1 := numList(min, 0, 59)
	hours, ok2 := numList(hour, 0, 23)
	if !ok1 || !ok2 || len(mins)*len(hours) > 4 {
		return fallback(expr)
	}
	var times []string
	for _, h := range hours {
		for _, m := range mins {
			times = append(times, fmt.Sprintf("%02d:%02d", h, m))
		}
	}
	at := " at " + joinAnd(times)

	switch {
	case allDays:
		return "Every day" + at
	case star(dom) && star(mon):
		days, ok := numList(dow, 0, 7)
		if !ok {
			return fallback(expr)
		}
		days = normalizeDow(days)
		switch {
		case equalInts(days, []int{1, 2, 3, 4, 5}):
			return "Every weekday" + at
		case equalInts(days, []int{0, 6}):
			return "Every weekend day" + at
		}
		names := make([]string, len(days))
		for i, d := range days {
			names[i] = dayNames[d]
		}
		return "Every " + joinAnd(names) + at
	case star(dow) && star(mon):
		days, ok := numList(dom, 1, 31)
		if !ok {
			return fallback(expr)
		}
		return "Monthly on the " + joinAnd(ordinals(days)) + at
	case star(dow) && isNum(mon) && isNum(dom):
		m := atoi(mon)
		if m < 1 || m > 12 {
			return fallback(expr)
		}
		return fmt.Sprintf("Every year on %s %d%s", monthNames[m], atoi(dom), at)
	}
	return fallback(expr)
}

func fallback(expr string) string { return "Custom schedule (" + expr + ")" }

func star(f string) bool { return f == "*" || f == "?" }

func isNum(f string) bool {
	_, err := strconv.Atoi(f)
	return err == nil
}

func atoi(f string) int { n, _ := strconv.Atoi(f); return n }

// stepOf returns N for "*/N", else 0.
func stepOf(f string) int {
	if s, ok := strings.CutPrefix(f, "*/"); ok {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func replaceNames(f string, alias map[string]string) string {
	for k, v := range alias {
		f = strings.ReplaceAll(f, k, v)
	}
	return f
}

// numList expands "1,3-5" into sorted distinct ints within [lo, hi].
func numList(f string, lo, hi int) ([]int, bool) {
	set := map[int]bool{}
	for _, part := range strings.Split(f, ",") {
		a, b, isRange := strings.Cut(part, "-")
		x, err := strconv.Atoi(a)
		if err != nil {
			return nil, false
		}
		y := x
		if isRange {
			if y, err = strconv.Atoi(b); err != nil || y < x {
				return nil, false
			}
		}
		if x < lo || y > hi {
			return nil, false
		}
		for i := x; i <= y; i++ {
			set[i] = true
		}
	}
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out, true
}

// normalizeDow maps 7 (Sunday) to 0 and re-sorts.
func normalizeDow(days []int) []int {
	set := map[int]bool{}
	for _, d := range days {
		set[d%7] = true
	}
	out := make([]int, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Ints(out)
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func ordinals(ns []int) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		suffix := "th"
		if n%100 < 11 || n%100 > 13 {
			switch n % 10 {
			case 1:
				suffix = "st"
			case 2:
				suffix = "nd"
			case 3:
				suffix = "rd"
			}
		}
		out[i] = strconv.Itoa(n) + suffix
	}
	return out
}

func plural(n int, unit string) string {
	if n == 1 {
		return unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func humanDuration(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return plural(int(d/(24*time.Hour)), "day")
	case d%time.Hour == 0:
		return plural(int(d/time.Hour), "hour")
	case d%time.Minute == 0:
		return plural(int(d/time.Minute), "minute")
	}
	return d.String()
}
