package api

func pageSlice[T any](items []T, skip, limit int32) []T {
	n := int64(len(items))
	s := int64(skip)
	if s >= n {
		return items[n:n]
	}
	e := s + int64(limit)
	if e > n {
		e = n
	}
	return items[s:e]
}
