package dbtest

import "testing"

func TestRefusesNonTestDatabase(t *testing.T) {
	cases := map[string]bool{
		"postgres://tixo:tixo@localhost:55432/tixo_test?sslmode=disable": true,
		"postgres://tixo:tixo@localhost:55432/tixo?sslmode=disable":      false,
		"postgres://u:p@prod-host:5432/ecommerce":                        false,
	}
	for raw, ok := range cases {
		if err := RequireTestDatabase(raw); (err == nil) != ok {
			t.Errorf("%s: err = %v, want allowed=%v", raw, err, ok)
		}
	}
}
