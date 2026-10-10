package i18n

import (
	"fmt"
	"time"
)

var spanishMonths = [...]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sept", "oct", "nov", "dic"}

// Clock is the time of day: "4:08 PM" in English, "16:08" in Spanish.
func Clock(t time.Time) string {
	if Lang() == Spanish {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}

// Date is a short date: "Sep 28" or "Sep 28, 2025" in English, "28 sept"
// or "28 sept 2025" in Spanish.
func Date(t time.Time, withYear bool) string {
	if Lang() == Spanish {
		s := fmt.Sprintf("%d %s", t.Day(), spanishMonths[t.Month()-1])
		if withYear {
			s += fmt.Sprintf(" %d", t.Year())
		}
		return s
	}
	if withYear {
		return t.Format("Jan 2, 2006")
	}
	return t.Format("Jan 2")
}
