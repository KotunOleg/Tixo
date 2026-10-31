package ticketpdf

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRenderProducesAValidUncompressedPDF(t *testing.T) {
	code := uuid.New()
	out, err := Render(TicketInfo{
		EventTitle: "Big Show",
		VenueName:  "Arena",
		VenueCity:  "Kyiv",
		TierName:   "Standard",
		StartsAt:   time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC),
		Code:       code,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("output does not start with %%PDF-, got %q", out[:min(16, len(out))])
	}
	s := string(out)
	for _, want := range []string{"Big Show", "Arena, Kyiv", "Standard", code.String()} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered pdf does not contain %q (SetCompression(false) should keep it inspectable)", want)
		}
	}
}

func TestRenderEmbedsAQRImage(t *testing.T) {
	out, err := Render(TicketInfo{EventTitle: "Show", Code: uuid.New()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.Contains(out, []byte("/Subtype /Image")) {
		t.Error("rendered pdf does not embed an image object for the QR code")
	}
}
