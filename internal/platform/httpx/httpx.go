package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
)

var ErrInvalidPagination = errors.New("skip and limit must be non-negative integers")

type ErrorBody struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields,omitempty"`
}

type Page struct {
	Items    any     `json:"items"`
	Count    int64   `json:"count"`
	Skip     int32   `json:"skip"`
	Limit    int32   `json:"limit"`
	NextLink *string `json:"nextLink"`
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, status int, message string, fields map[string]string) {
	WriteJSON(w, status, ErrorBody{Error: message, Fields: fields})
}

func Paginate(r *http.Request) (skip, limit int32, err error) {
	q := r.URL.Query()
	skip, limit = 0, DefaultLimit
	if v := q.Get("skip"); v != "" {
		n, convErr := strconv.ParseInt(v, 10, 32)
		if convErr != nil || n < 0 {
			return 0, 0, ErrInvalidPagination
		}
		skip = int32(n)
	}
	if v := q.Get("limit"); v != "" {
		n, convErr := strconv.ParseInt(v, 10, 32)
		if convErr != nil || n < 0 {
			return 0, 0, ErrInvalidPagination
		}
		limit = int32(n)
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	return skip, limit, nil
}

func Envelope(items any, count int64, skip, limit int32, r *http.Request) Page {
	page := Page{Items: nonNilSlice(items), Count: count, Skip: skip, Limit: limit}
	if int64(skip)+int64(limit) < count {
		q := r.URL.Query()
		q.Set("skip", strconv.FormatInt(int64(skip)+int64(limit), 10))
		q.Set("limit", strconv.FormatInt(int64(limit), 10))
		u := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
		link := u.String()
		page.NextLink = &link
	}
	return page
}

func nonNilSlice(items any) any {
	v := reflect.ValueOf(items)
	if v.Kind() == reflect.Slice && v.IsNil() {
		return reflect.MakeSlice(v.Type(), 0, 0).Interface()
	}
	return items
}
