package escpos

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"strings"
	"time"
)

// GenericLine represents a single formatted line in a generic receipt layout.
type GenericLine struct {
	Type   string `json:"type"`          // "logo", "text", "columns", "divider", "feed", "cut", "drawer"
	Text   string `json:"text,omitempty"` // For "text" type
	Align  string `json:"align,omitempty"` // "LEFT", "CENTER", "RIGHT"
	Size   string `json:"size,omitempty"`  // "NORMAL", "DOUBLE_HEIGHT", "DOUBLE_WIDTH", "DOUBLE_HW"
	Bold   bool   `json:"bold,omitempty"`
	Left   string `json:"left,omitempty"`  // For "columns" type
	Right  string `json:"right,omitempty"` // For "columns" type
	Char   string `json:"char,omitempty"`  // For "divider" type (default: "-")
	Lines  int    `json:"lines,omitempty"` // For "feed" type (default: 1)
}

// GenericReceipt represents a layout-free generic receipt payload.
type GenericReceipt struct {
	PrinterName string        `json:"printerName"`
	Width       int           `json:"width"` // e.g., 32 or 48
	Lines       []GenericLine `json:"lines"`
}

// ESC/POS Command Constants
var (
	// CmdInit initializes the printer (ESC @)
	CmdInit = []byte{0x1b, 0x40}

	// Alignment commands (ESC a n)
	CmdAlignLeft   = []byte{0x1b, 0x61, 0x00}
	CmdAlignCenter = []byte{0x1b, 0x61, 0x01}
	CmdAlignRight  = []byte{0x1b, 0x61, 0x02}

	// Font style commands (ESC E n)
	CmdBoldOn  = []byte{0x1b, 0x45, 0x01}
	CmdBoldOff = []byte{0x1b, 0x45, 0x00}

	// Font size commands (GS ! n)
	CmdSizeNormal   = []byte{0x1d, 0x21, 0x00} // 1x width, 1x height
	CmdSizeDoubleH  = []byte{0x1d, 0x21, 0x01} // 1x width, 2x height
	CmdSizeDoubleW  = []byte{0x1d, 0x21, 0x10} // 2x width, 1x height
	CmdSizeDoubleHW = []byte{0x1d, 0x21, 0x11} // 2x width, 2x height

	// Cash Drawer command (ESC p m t1 t2)
	// Pin 2 drawer kick: 1b 70 00 19 fa (m=0, t1=25, t2=250)
	CmdCashDrawer = []byte{0x1b, 0x70, 0x00, 0x19, 0xfc}

	// Cut command (GS V m n)
	// Feed and partial cut (m=66, n=0) -> feeds paper to cutting position and cuts
	CmdCutPartial = []byte{0x1d, 0x56, 0x42, 0x00}
)

const PageWidth = 32 // 58mm printer page width in characters

// Item represents a single line item in the receipt
type Item struct {
	Name  string `json:"name"`
	Qty   int    `json:"qty"`
	Price int    `json:"price"`
	Total int    `json:"total"`
}

// Receipt represents the complete structured receipt data
type Receipt struct {
	PrinterName   string `json:"printerName"`
	StoreName     string `json:"storeName"`
	Cashier       string `json:"cashier"`
	InvoiceNo     string `json:"invoiceNo"`
	Items         []Item `json:"items"`
	Subtotal      int    `json:"subtotal"`
	Discount      int    `json:"discount"`
	Tax           int    `json:"tax"`
	GrandTotal    int    `json:"grandTotal"`
	PaymentMethod string `json:"paymentMethod"`
	Paid          int    `json:"paid"`
	Change        int    `json:"change"`
	Footer        string `json:"footer"`
	KickDrawer    bool   `json:"kickDrawer"` // Whether to trigger cash drawer kick
	SkipCut       bool   `json:"skipCut"`    // Option to skip automatic cutting
	UseLogo       bool   `json:"useLogo"`    // Prepend cached logo.bin if present
	Width         int    `json:"width"`      // Optional line width override: e.g. 32 (58mm) or 48 (80mm)
}

// FeedLines creates command to feed n lines (ESC d n)
func FeedLines(n int) []byte {
	if n <= 0 {
		return nil
	}
	return []byte{0x1b, 0x64, byte(n)}
}

// FormatRupiah formats an integer as Indonesian Rupiah currency (e.g. 15000 -> "Rp 15.000")
func FormatRupiah(val int) string {
	negative := false
	if val < 0 {
		negative = true
		val = -val
	}

	str := ""
	if val == 0 {
		str = "0"
	} else {
		for val > 0 {
			rem := val % 1000
			val = val / 1000
			if val > 0 {
				str = fmt.Sprintf(".%03d%s", rem, str)
			} else {
				str = fmt.Sprintf("%d%s", rem, str)
			}
		}
	}

	if negative {
		return "-Rp " + str
	}
	return "Rp " + str
}

// WrapText wraps text to a max width, keeping words intact, splitting long words if necessary.
func WrapText(text string, width int) []string {
	if len(text) == 0 {
		return []string{""}
	}
	var lines []string
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}
	currentLine := ""
	for _, word := range words {
		// Handle extremely long words that exceed the line width
		if len(word) > width {
			if currentLine != "" {
				lines = append(lines, currentLine)
				currentLine = ""
			}
			for len(word) > width {
				lines = append(lines, word[:width])
				word = word[width:]
			}
			currentLine = word
			continue
		}

		if currentLine == "" {
			currentLine = word
		} else if len(currentLine)+1+len(word) <= width {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}
	return lines
}

// FormatLine formats a line with a left-aligned portion and a right-aligned portion, padded with spaces.
func FormatLine(left, right string, width int) string {
	spaceNeed := width - len(left) - len(right)
	if spaceNeed <= 0 {
		// Fallback: separate by at least one space if it overflows
		return left + " " + right
	}
	return left + strings.Repeat(" ", spaceNeed) + right
}

// GenerateESCPOSText compiles the receipt structure into a raw ESC/POS byte slice
func GenerateESCPOSText(r Receipt) ([]byte, error) {
	var buf bytes.Buffer

	// Resolve target width (default to 32 characters if none supplied)
	width := r.Width
	if width <= 0 {
		width = 32
	}

	// 1. Initialize printer
	buf.Write(CmdInit)

	// 2. Open Cash Drawer if requested
	if r.KickDrawer {
		buf.Write(CmdCashDrawer)
	}

	// 2.5 Prepend local logo if cached and requested
	if r.UseLogo {
		if logoBytes, err := os.ReadFile("logo.bin"); err == nil {
			buf.Write(CmdAlignCenter)
			buf.Write(logoBytes)
			buf.Write(FeedLines(1))
		}
	}

	// 3. Store Name (Double size, bold, centered)
	buf.Write(CmdAlignCenter)
	buf.Write(CmdSizeDoubleHW)
	buf.Write(CmdBoldOn)
	buf.WriteString(r.StoreName + "\n")

	// Restore styling to normal
	buf.Write(CmdSizeNormal)
	buf.Write(CmdBoldOff)

	// Sub-spacing
	buf.Write(FeedLines(1))

	// 4. Receipt details (Left-aligned)
	buf.Write(CmdAlignLeft)
	
	// Format current time in local layout
	currentTime := time.Now().Format("02-01-2006 15:04:52")
	
	buf.WriteString(fmt.Sprintf("Date   : %s\n", currentTime))
	buf.WriteString(fmt.Sprintf("Invoice: %s\n", r.InvoiceNo))
	buf.WriteString(fmt.Sprintf("Cashier: %s\n", r.Cashier))

	// Divider
	buf.WriteString(strings.Repeat("-", width) + "\n")

	// 5. Items list
	for _, item := range r.Items {
		// Wrap name nicely to column width
		nameLines := WrapText(item.Name, width)
		for i, line := range nameLines {
			// If it's the last line of the name and item only has 1 qty, we can try inline.
			// However, to keep it extremely neat, clean, and perfectly aligned, we will:
			// - Print the entire item name first
			// - Print the details line (qty, price, and total) below it
			buf.WriteString(line + "\n")
			_ = i
		}

		// Details line: "  1 x Rp 10.000      Rp 10.000"
		qtyPrice := fmt.Sprintf("  %d x %s", item.Qty, FormatRupiah(item.Price))
		totalPrice := FormatRupiah(item.Total)
		buf.WriteString(FormatLine(qtyPrice, totalPrice, width) + "\n")
	}

	// Divider
	buf.WriteString(strings.Repeat("-", width) + "\n")

	// 6. Totals section
	buf.WriteString(FormatLine("Subtotal", FormatRupiah(r.Subtotal), width) + "\n")
	if r.Discount > 0 {
		buf.WriteString(FormatLine("Discount", "-"+FormatRupiah(r.Discount), width) + "\n")
	}
	if r.Tax > 0 {
		buf.WriteString(FormatLine("Tax", FormatRupiah(r.Tax), width) + "\n")
	}
	
	// Divider
	buf.WriteString(strings.Repeat("-", width) + "\n")

	// Grand Total (Bold)
	buf.Write(CmdBoldOn)
	buf.WriteString(FormatLine("GRAND TOTAL", FormatRupiah(r.GrandTotal), width) + "\n")
	buf.Write(CmdBoldOff)

	buf.WriteString(strings.Repeat("-", width) + "\n")

	// 7. Payment details
	buf.WriteString(FormatLine("Payment", r.PaymentMethod, width) + "\n")
	buf.WriteString(FormatLine("Paid", FormatRupiah(r.Paid), width) + "\n")
	buf.WriteString(FormatLine("Change", FormatRupiah(r.Change), width) + "\n")

	buf.WriteString(strings.Repeat("-", width) + "\n")

	// 8. Footer (Centered)
	if r.Footer != "" {
		buf.Write(CmdAlignCenter)
		footerLines := WrapText(r.Footer, width)
		for _, line := range footerLines {
			buf.WriteString(line + "\n")
		}
		buf.Write(CmdAlignLeft)
	}

	// 9. Feed lines and Cut
	buf.Write(FeedLines(2)) // Feed 2 lines (compact for 58mm) so text is above cutter
	if !r.SkipCut {
		buf.Write(CmdCutPartial)
	}

	return buf.Bytes(), nil
}

// TaskTicket represents structured data for a job task order.
type TaskTicket struct {
	PrinterName string `json:"printerName"`
	TaskID      string `json:"taskId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"` // LOW, MEDIUM, HIGH
	Assignee    string `json:"assignee"`
	DueDate     string `json:"dueDate"`
	KickDrawer  bool   `json:"kickDrawer"`
	SkipCut     bool   `json:"skipCut"`
	Width       int    `json:"width"` // Optional line width override: e.g. 32 (58mm) or 48 (80mm)
}

// GenerateTaskTicket compiles task details into standard 58mm ESC/POS task order tickets.
func GenerateTaskTicket(t TaskTicket) ([]byte, error) {
	var buf bytes.Buffer

	// Resolve target width (default to 32 characters if none supplied)
	width := t.Width
	if width <= 0 {
		width = 32
	}

	// 1. Initialize printer
	buf.Write(CmdInit)

	// 2. Open Cash Drawer if requested
	if t.KickDrawer {
		buf.Write(CmdCashDrawer)
	}

	// 3. Title Header (Double height/width, bold, centered)
	buf.Write(CmdAlignCenter)
	buf.Write(CmdSizeDoubleHW)
	buf.Write(CmdBoldOn)
	buf.WriteString("TASK TICKET\n")
	buf.Write(CmdSizeNormal)
	buf.Write(CmdBoldOff)

	// Sub-border
	buf.WriteString(strings.Repeat("=", width) + "\n")
	buf.Write(CmdAlignLeft)

	// Metadata
	buf.WriteString(fmt.Sprintf("Ticket ID: #%s\n", t.TaskID))
	buf.WriteString(fmt.Sprintf("Assignee : %s\n", t.Assignee))
	if t.DueDate != "" {
		buf.WriteString(fmt.Sprintf("Due Date : %s\n", t.DueDate))
	} else {
		buf.WriteString(fmt.Sprintf("Date     : %s\n", time.Now().Format("02-01-2006 15:04")))
	}

	// Priority badge formatting
	prioStr := strings.ToUpper(t.Priority)
	if prioStr == "" {
		prioStr = "MEDIUM"
	}
	buf.WriteString("Priority : ")
	if prioStr == "HIGH" {
		buf.Write(CmdBoldOn)
		buf.WriteString("*** HIGH ***\n")
		buf.Write(CmdBoldOff)
	} else {
		buf.WriteString(prioStr + "\n")
	}

	buf.WriteString(strings.Repeat("-", width) + "\n")

	// Task Title
	buf.Write(CmdBoldOn)
	titleLines := WrapText(t.Title, width)
	for _, line := range titleLines {
		buf.WriteString(line + "\n")
	}
	buf.Write(CmdBoldOff)
	buf.WriteString("\n")

	// Description (if provided)
	if t.Description != "" {
		descLines := WrapText(t.Description, width)
		for _, line := range descLines {
			buf.WriteString(line + "\n")
		}
		buf.WriteString(strings.Repeat("-", width) + "\n")
	}

	// Checkoff items for workforce
	buf.WriteString("[ ] Checked In  : _____:_____\n")
	buf.WriteString("[ ] Completed   : _____:_____\n")
	buf.WriteString("\n")
	buf.WriteString("Sign Worker: ________________\n")
	buf.WriteString(strings.Repeat("=", width) + "\n")

	// Feed lines and cut
	buf.Write(FeedLines(2)) // Feed 2 lines (compact for 58mm) so text is above cutter
	if !t.SkipCut {
		buf.Write(CmdCutPartial)
	}

	return buf.Bytes(), nil
}

// CompileLogo decodes an image, resizes it to 384px width, and compiles it to ESC/POS monochrome bit raster bytes (GS v 0).
func CompileLogo(r io.Reader) ([]byte, error) {
	img, _, err := image.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	bounds := img.Bounds()
	srcWidth := bounds.Dx()
	srcHeight := bounds.Dy()
	if srcWidth == 0 || srcHeight == 0 {
		return nil, fmt.Errorf("invalid image dimensions")
	}

	// Target 58mm printer dot width: exactly 384 pixels
	targetWidth := 384
	// Calculate proportional height
	targetHeight := int(float64(targetWidth) / float64(srcWidth) * float64(srcHeight))

	// Scale image using high-performance Nearest-Neighbor interpolation (100% CGO-free Go standard library)
	scaled := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		for x := 0; x < targetWidth; x++ {
			srcX := bounds.Min.X + int(float64(x)/float64(targetWidth)*float64(srcWidth))
			srcY := bounds.Min.Y + int(float64(y)/float64(targetHeight)*float64(srcHeight))
			scaled.Set(x, y, img.At(srcX, srcY))
		}
	}

	// Target bytes per line: 384 / 8 = 48 bytes
	widthBytes := targetWidth / 8
	var buf bytes.Buffer

	// GS v 0 m xL xH yL yH
	// m = 0 (Normal density), xL/xH = width in bytes, yL/yH = height in pixels
	buf.Write([]byte{0x1d, 0x76, 0x30, 0x00})
	buf.WriteByte(byte(widthBytes & 0xff))
	buf.WriteByte(byte((widthBytes >> 8) & 0xff))
	buf.WriteByte(byte(targetHeight & 0xff))
	buf.WriteByte(byte((targetHeight >> 8) & 0xff))

	// Rasterize pixels to 1-bit monochrome data
	for y := 0; y < targetHeight; y++ {
		for xByte := 0; xByte < widthBytes; xByte++ {
			var byteVal byte = 0
			for bit := 0; bit < 8; bit++ {
				x := xByte*8 + bit
				c := scaled.At(x, y)
				r, g, b, a := c.RGBA()
				
				// Standard luminosity weights: transparent pixel is white, colored is thresholded
				alpha := a >> 8
				gray := uint32(255)
				if alpha > 128 {
					gray = (r*299 + g*587 + b*114) / 1000 >> 8
				}
				
				// Threshold check: darker than 128 is black (bit set to 1)
				if gray < 128 {
					byteVal |= (1 << (7 - uint(bit)))
				}
			}
			buf.WriteByte(byteVal)
		}
	}

	// Trailing feeds to clear print head
	buf.Write([]byte{0x1b, 0x64, 0x01})

	return buf.Bytes(), nil
}

// GenerateGenericReceipt compiles a list of custom structured lines into raw monospaced ESC/POS.
func GenerateGenericReceipt(gr GenericReceipt) ([]byte, error) {
	var buf bytes.Buffer

	// Resolve width
	width := gr.Width
	if width <= 0 {
		width = 32
	}

	// 1. Initialize printer
	buf.Write(CmdInit)

	// 2. Process each line sequentially
	for _, line := range gr.Lines {
		switch strings.ToLower(line.Type) {
		case "logo":
			if logoBytes, err := os.ReadFile("logo.bin"); err == nil {
				buf.Write(CmdAlignCenter)
				buf.Write(logoBytes)
				buf.Write(FeedLines(1))
			}

		case "text":
			// Alignment
			switch strings.ToUpper(line.Align) {
			case "CENTER":
				buf.Write(CmdAlignCenter)
			case "RIGHT":
				buf.Write(CmdAlignRight)
			default:
				buf.Write(CmdAlignLeft)
			}

			// Size
			switch strings.ToUpper(line.Size) {
			case "DOUBLE_HEIGHT":
				buf.Write(CmdSizeDoubleH)
			case "DOUBLE_WIDTH":
				buf.Write(CmdSizeDoubleW)
			case "DOUBLE_HW":
				buf.Write(CmdSizeDoubleHW)
			default:
				buf.Write(CmdSizeNormal)
			}

			// Bold
			if line.Bold {
				buf.Write(CmdBoldOn)
			} else {
				buf.Write(CmdBoldOff)
			}

			// Wrap text to fit printer columns
			wrapped := WrapText(line.Text, width)
			for _, wLine := range wrapped {
				buf.WriteString(wLine + "\n")
			}

			// Restore default settings
			buf.Write(CmdSizeNormal)
			buf.Write(CmdBoldOff)
			buf.Write(CmdAlignLeft)

		case "columns":
			// Size
			switch strings.ToUpper(line.Size) {
			case "DOUBLE_HEIGHT":
				buf.Write(CmdSizeDoubleH)
			case "DOUBLE_WIDTH":
				buf.Write(CmdSizeDoubleW)
			case "DOUBLE_HW":
				buf.Write(CmdSizeDoubleHW)
			default:
				buf.Write(CmdSizeNormal)
			}

			// Bold
			if line.Bold {
				buf.Write(CmdBoldOn)
			} else {
				buf.Write(CmdBoldOff)
			}

			buf.WriteString(FormatLine(line.Left, line.Right, width) + "\n")

			// Restore
			buf.Write(CmdSizeNormal)
			buf.Write(CmdBoldOff)

		case "divider":
			char := line.Char
			if char == "" {
				char = "-"
			}
			buf.WriteString(strings.Repeat(char, width) + "\n")

		case "feed":
			count := line.Lines
			if count <= 0 {
				count = 1
			}
			buf.Write(FeedLines(count))

		case "drawer":
			buf.Write(CmdCashDrawer)

		case "cut":
			buf.Write(CmdCutPartial)
		}
	}

	return buf.Bytes(), nil
}

