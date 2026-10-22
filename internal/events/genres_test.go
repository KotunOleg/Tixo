package events

import "testing"

func TestIsGenreAcceptsOnlyTheFixedList(t *testing.T) {
	cases := map[string]bool{
		GenreRock:   true,
		GenreHipHop: true,
		"Rock":      false,
		" rock":     false,
		"":          false,
		"polka":     false,
	}
	for g, want := range cases {
		if got := IsGenre(g); got != want {
			t.Errorf("IsGenre(%q) = %v, want %v", g, got, want)
		}
	}
}

func TestGenresReturnsACopy(t *testing.T) {
	Genres()[0] = "hacked"
	if !IsGenre(GenreRock) || IsGenre("hacked") {
		t.Error("callers can modify the genre list through Genres()")
	}
}
