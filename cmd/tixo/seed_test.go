package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestSeedRefusedInProd(t *testing.T) {
	setValidProdEnv(t)
	err := run(context.Background(), []string{"seed"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, errSeedInProd) {
		t.Errorf("seed in prod: err = %v, want refusal", err)
	}
}
