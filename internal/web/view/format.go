package view

import (
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/KotunOleg/Tixo/internal/web/i18n"
)

const nbsp = " "

var (
	displayZone = mustZone("Europe/Kyiv")
	monthKeys   = [...]string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
)

func mustZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func (p *Page) Date(t time.Time) (string, error) {
	local := t.In(displayZone)
	month, err := p.T("date.month." + monthKeys[local.Month()-1])
	if err != nil {
		return "", err
	}
	return p.T("date.day", "Day", local.Day(), "Month", month, "Year", local.Year())
}

func (p *Page) DateTime(t time.Time) (string, error) {
	day, err := p.Date(t)
	if err != nil {
		return "", err
	}
	return p.T("date.at", "Date", day, "Time", t.In(displayZone).Format("15:04"))
}

func (p *Page) When(start, end time.Time) (string, error) {
	s, e := start.In(displayZone), end.In(displayZone)
	if e.Sub(s) < 24*time.Hour {
		day, err := p.Date(s)
		if err != nil {
			return "", err
		}
		return p.T("date.at", "Date", day, "Time", s.Format("15:04")+"–"+e.Format("15:04"))
	}
	from, err := p.DateTime(s)
	if err != nil {
		return "", err
	}
	to, err := p.DateTime(e)
	if err != nil {
		return "", err
	}
	return from + " – " + to, nil
}

func (p *Page) Money(kopecks int64) (string, error) {
	if kopecks == 0 {
		return p.T("money.free")
	}
	group, decimal := ",", "."
	if p.Lang == i18n.UK {
		group, decimal = nbsp, ","
	}
	n := groupDigits(strconv.FormatInt(kopecks/100, 10), group)
	if cents := kopecks % 100; cents != 0 {
		n += decimal + pad2(cents)
	}
	if p.Lang == i18n.UK {
		return n + nbsp + "₴", nil
	}
	return "₴" + n, nil
}

func groupDigits(digits, sep string) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	head := len(digits) % 3
	if head > 0 {
		b.WriteString(digits[:head])
	}
	for i := head; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteString(sep)
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

func pad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}
