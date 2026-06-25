package pdfprinter

import (
	"fmt"
	"strings"
	"time"

	"github.com/jung-kurt/gofpdf"
)

// GenerateReceiptPDF compiles receipt text into a PDF file
// styled to look like a 58mm thermal receipt.
func GenerateReceiptPDF(text string, outputPath string) error {
	// 58mm thermal paper: ~32mm printable width
	pageW := 32.0
	pageH := 200.0 // initial height, will be trimmed

	init := &gofpdf.InitType{
		OrientationStr: "P",
		UnitStr:        "mm",
		SizeStr:        "",
		Size:           gofpdf.SizeType{Wd: pageW, Ht: pageH},
		FontDirStr:     "",
	}
	pdf := gofpdf.NewCustom(init)
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddPage()

	// White background
	pdf.SetFillColor(255, 255, 255)
	pdf.Rect(0, 0, pageW, pageH, "F")

	// Monospace font for receipt look
	pdf.SetFont("Courier", "", 7)
	pdf.SetTextColor(0, 0, 0)
	pdf.SetDrawColor(0, 0, 0)

	marginX := 2.0
	lineH := 3.2
	usableW := pageW - (marginX * 2)

	lines := strings.Split(text, "\n")

	for _, line := range lines {
		cleanLine := stripEscCommands(line)

		// Divider: draw a horizontal line
		if isDivider(cleanLine) {
			y := pdf.GetY()
			pdf.SetLineWidth(0.3)
			pdf.Line(marginX, y+lineH/2, pageW-marginX, y+lineH/2)
			pdf.Ln(lineH)
			continue
		}

		trimmed := strings.TrimLeft(cleanLine, " ")
		leadingSpaces := len(cleanLine) - len(trimmed)

		// If leading spaces > 5, treat as centered
		if leadingSpaces > 5 && len(trimmed) > 0 {
			pdf.SetXY(marginX, pdf.GetY())
			pdf.CellFormat(usableW, lineH, trimmed, "", 1, "C", false, 0, "")
		} else if len(cleanLine) > 0 {
			pdf.SetXY(marginX, pdf.GetY())
			pdf.CellFormat(usableW, lineH, cleanLine, "", 1, "L", false, 0, "")
		} else {
			pdf.Ln(lineH)
		}
	}

	// Dashed bottom line (paper tear simulation)
	pdf.SetDrawColor(180, 180, 180)
	pdf.SetLineWidth(0.2)
	pdf.SetDashPattern([]float64{1.5, 1.0}, 0)
	y := pdf.GetY() + 2
	pdf.Line(marginX, y, pageW-marginX, y)

	// Close and save
	err := pdf.OutputFileAndClose(outputPath)
	if err != nil {
		return fmt.Errorf("failed to save PDF: %w", err)
	}

	return nil
}

// DefaultOutputPath returns a default PDF output path with timestamp.
func DefaultOutputPath() string {
	timestamp := time.Now().Format("20060102_150405")
	return fmt.Sprintf("receipt_%s.pdf", timestamp)
}

// stripEscCommands removes ESC/POS byte sequences from text, leaving readable content.
func stripEscCommands(s string) string {
	var result []byte
	inEscape := false

	for i := 0; i < len(s); i++ {
		b := s[i]

		if inEscape {
			switch b {
			case 0x40: // ESC @
				inEscape = false
			case 0x61: // ESC a n
				if i+1 < len(s) {
					i++
				}
				inEscape = false
			case 0x45: // ESC E n
				if i+1 < len(s) {
					i++
				}
				inEscape = false
			case 0x64: // ESC d n
				if i+1 < len(s) {
					i++
				}
				inEscape = false
			case 0x70: // ESC p m t1 t2
				if i+3 < len(s) {
					i += 3
				}
				inEscape = false
			default:
				inEscape = false
			}
			continue
		}

		if b == 0x1b || b == 0x1d {
			if b == 0x1d {
				if i+1 < len(s) {
					next := s[i+1]
					switch next {
					case 0x21: // GS ! n
						if i+2 < len(s) {
							i += 2
						}
						continue
					case 0x56: // GS V m n
						if i+3 < len(s) {
							i += 3
						}
						continue
					case 0x76: // GS v (raster)
						return "" // can't represent images as text
					}
				}
				continue
			}
			inEscape = true
			continue
		}

		result = append(result, b)
	}

	return string(result)
}

// isDivider checks if a line is a horizontal divider (e.g., "---" or "===").
func isDivider(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 5 {
		return false
	}
	first := s[0]
	for _, c := range s {
		if byte(c) != first {
			return false
		}
	}
	return first == '-' || first == '=' || first == '_' || first == '~' || first == '*'
}
