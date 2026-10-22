package events

import "slices"

const (
	GenreRock        = "rock"
	GenrePop         = "pop"
	GenreIndie       = "indie"
	GenreAlternative = "alternative"
	GenreElectronic  = "electronic"
	GenreHipHop      = "hip-hop"
	GenreFolk        = "folk"
	GenreJazz        = "jazz"
	GenreClassical   = "classical"
	GenreMetal       = "metal"
)

var genres = []string{
	GenreRock, GenrePop, GenreIndie, GenreAlternative, GenreElectronic,
	GenreHipHop, GenreFolk, GenreJazz, GenreClassical, GenreMetal,
}

func Genres() []string { return slices.Clone(genres) }

func IsGenre(g string) bool { return slices.Contains(genres, g) }
