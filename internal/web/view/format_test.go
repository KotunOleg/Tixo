package view

import (
	"testing"
	"testing/fstest"
	"time"

	"github.com/KotunOleg/Tixo/internal/web/i18n"
)

const ukDates = `[date]
day = "{{.Day}} {{.Month}} {{.Year}}"
at = "{{.Date}}, {{.Time}}"
[date.month]
jan = "січня"
feb = "лютого"
mar = "березня"
apr = "квітня"
may = "травня"
jun = "червня"
jul = "липня"
aug = "серпня"
sep = "вересня"
oct = "жовтня"
nov = "листопада"
dec = "грудня"
[money]
free = "Безкоштовно"
`

const enDates = `[date]
day = "{{.Month}} {{.Day}}, {{.Year}}"
at = "{{.Date}}, {{.Time}}"
[date.month]
jan = "January"
feb = "February"
mar = "March"
apr = "April"
may = "May"
jun = "June"
jul = "July"
aug = "August"
sep = "September"
oct = "October"
nov = "November"
dec = "December"
[money]
free = "Free"
`

func formatPages(t *testing.T) map[i18n.Lang]*Page {
	t.Helper()
	tr, err := i18n.New(fstest.MapFS{
		"locales/uk.toml": {Data: []byte(ukDates)},
		"locales/en.toml": {Data: []byte(enDates)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return map[i18n.Lang]*Page{i18n.UK: {Lang: i18n.UK, tr: tr}, i18n.EN: {Lang: i18n.EN, tr: tr}}
}

func TestMoneyUsesLanguageSeparatorsAndHryvniaSign(t *testing.T) {
	pages := formatPages(t)
	cases := []struct {
		lang    i18n.Lang
		kopecks int64
		want    string
	}{
		{i18n.UK, 35000, "350 ₴"},
		{i18n.UK, 125050, "1 250,50 ₴"},
		{i18n.UK, 123456789, "1 234 567,89 ₴"},
		{i18n.EN, 35000, "₴350"},
		{i18n.EN, 125005, "₴1,250.05"},
		{i18n.EN, 0, "Free"},
		{i18n.UK, 0, "Безкоштовно"},
	}
	for _, tc := range cases {
		got, err := pages[tc.lang].Money(tc.kopecks)
		if err != nil || got != tc.want {
			t.Errorf("%s Money(%d) = %q, %v; want %q", tc.lang, tc.kopecks, got, err, tc.want)
		}
	}
}

func TestDatesAreShownInKyivTimeAndLanguage(t *testing.T) {
	pages := formatPages(t)
	start := time.Date(2030, 6, 12, 16, 0, 0, 0, time.UTC)
	cases := []struct {
		lang i18n.Lang
		end  time.Time
		want string
	}{
		{i18n.UK, start.Add(2 * time.Hour), "12 червня 2030, 19:00–21:00"},
		{i18n.EN, start.Add(2 * time.Hour), "June 12, 2030, 19:00–21:00"},
		{i18n.UK, start.Add(7 * time.Hour), "12 червня 2030, 19:00–02:00"},
		{i18n.UK, start.Add(30 * time.Hour), "12 червня 2030, 19:00 – 14 червня 2030, 01:00"},
	}
	for _, tc := range cases {
		got, err := pages[tc.lang].When(start, tc.end)
		if err != nil || got != tc.want {
			t.Errorf("%s When = %q, %v; want %q", tc.lang, got, err, tc.want)
		}
	}
}
