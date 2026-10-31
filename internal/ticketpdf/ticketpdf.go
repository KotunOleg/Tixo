package ticketpdf

import (
	"bytes"
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"
)

const qrSizePx = 256

type TicketInfo struct {
	EventTitle string
	VenueName  string
	VenueCity  string
	TierName   string
	StartsAt   time.Time
	Code       uuid.UUID
}

func Render(info TicketInfo) ([]byte, error) {
	qr, err := qrcode.Encode(info.Code.String(), qrcode.Medium, qrSizePx)
	if err != nil {
		return nil, fmt.Errorf("ticketpdf: encode qr: %w", err)
	}

	pdf := fpdf.New("P", "mm", "A5", "")
	pdf.SetCompression(false)
	pdf.AddPage()

	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(0, 10, info.EventTitle, "", 1, "L", false, 0, "")

	pdf.SetFont("Helvetica", "", 12)
	pdf.CellFormat(0, 8, fmt.Sprintf("%s, %s", info.VenueName, info.VenueCity), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 8, info.StartsAt.Format("2006-01-02 15:04"), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 8, info.TierName, "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 8, info.Code.String(), "", 1, "L", false, 0, "")

	imgOpts := fpdf.ImageOptions{ImageType: "PNG"}
	pdf.RegisterImageOptionsReader("qr", imgOpts, bytes.NewReader(qr))
	pdf.ImageOptions("qr", 10, pdf.GetY()+5, 40, 40, false, imgOpts, 0, "")

	if err := pdf.Error(); err != nil {
		return nil, fmt.Errorf("ticketpdf: build pdf: %w", err)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("ticketpdf: render pdf: %w", err)
	}
	return buf.Bytes(), nil
}
