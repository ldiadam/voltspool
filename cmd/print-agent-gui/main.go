package main

import (
	"fmt"
	"image/color"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"print-agent/internal/config"
	"print-agent/internal/escpos"
	"print-agent/internal/pdfprinter"
	"print-agent/internal/printer"
	"print-agent/internal/server"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ─────────────────────────────────────────────────────────────
// Enterprise Theme — Slate Dark with Teal accent
// ─────────────────────────────────────────────────────────────

type enterpriseTheme struct{}

var (
	// Core palette
	colBg       = color.RGBA{R: 15, G: 23, B: 42, A: 255}    // slate-900
	colSurface  = color.RGBA{R: 30, G: 41, B: 59, A: 255}    // slate-800
	colCard     = color.RGBA{R: 30, G: 41, B: 59, A: 255}    // slate-800
	colInput    = color.RGBA{R: 51, G: 65, B: 85, A: 255}    // slate-700
	colBorder   = color.RGBA{R: 51, G: 65, B: 85, A: 255}    // slate-700
	colPrimary  = color.RGBA{R: 20, G: 184, B: 166, A: 255}  // teal-500
	colPrimaryD = color.RGBA{R: 13, G: 148, B: 136, A: 255}  // teal-600
	colFg       = color.RGBA{R: 241, G: 245, B: 249, A: 255} // slate-100
	colFgDim    = color.RGBA{R: 148, G: 163, B: 184, A: 255} // slate-400
	colFgMuted  = color.RGBA{R: 100, G: 116, B: 139, A: 255} // slate-500
	colSuccess  = color.RGBA{R: 34, G: 197, B: 94, A: 255}   // green-500
	colWarning  = color.RGBA{R: 234, G: 179, B: 8, A: 255}   // yellow-500
	colDanger   = color.RGBA{R: 239, G: 68, B: 68, A: 255}   // red-500
	colBtnOff   = color.RGBA{R: 30, G: 41, B: 59, A: 255}    // slate-800
	colBtnHov   = color.RGBA{R: 51, G: 65, B: 85, A: 255}    // slate-700
)

func (e enterpriseTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return colBg
	case theme.ColorNameInputBackground:
		return colInput
	case theme.ColorNameButton:
		return colBtnOff
	case theme.ColorNamePrimary:
		return colPrimary
	case theme.ColorNameForeground:
		return colFg
	case theme.ColorNamePlaceHolder:
		return colFgMuted
	case theme.ColorNameSeparator:
		return colBorder
	case theme.ColorNameDisabledButton:
		return colSurface
	case theme.ColorNameOverlayBackground:
		return colCard
	case theme.ColorNameHeaderBackground:
		return colSurface
	case theme.ColorNameScrollBar:
		return colFgMuted
	}
	return theme.DefaultTheme().Color(n, v)
}

func (e enterpriseTheme) Font(s fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(s)
}

func (e enterpriseTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (e enterpriseTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 12.0
	case theme.SizeNamePadding:
		return 10.0
	case theme.SizeNameInnerPadding:
		return 8.0
	case theme.SizeNameScrollBar:
		return 6.0
	case theme.SizeNameScrollBarSmall:
		return 4.0
	case theme.SizeNameSeparatorThickness:
		return 1
	}
	return theme.DefaultTheme().Size(n)
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

type LogRedirector struct {
	mu   sync.Mutex
	logs []string
}

func (lr *LogRedirector) Write(p []byte) (n int, err error) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	lr.logs = append(lr.logs, fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), string(p)))
	return len(p), nil
}

type GUIUpdate struct {
	Type  string
	Value string
}

var (
	srvInstance    *server.Server
	serverMutex    sync.Mutex
	isServerActive bool
	guiUpdateChan  = make(chan GUIUpdate, 50)
)

// ─────────────────────────────────────────────────────────────
// Receipt text helpers
// ─────────────────────────────────────────────────────────────

func formatLine(left, right string, width int) string {
	spaceNeed := width - len(left) - len(right)
	if spaceNeed <= 0 {
		return left + " " + right
	}
	return left + strings.Repeat(" ", spaceNeed) + right
}

func wrapText(text string, width int) []string {
	if len(text) <= width {
		return []string{text}
	}
	words := strings.Split(text, " ")
	var lines []string
	currentLine := ""
	for _, word := range words {
		if len(currentLine)+1+len(word) <= width {
			if currentLine == "" {
				currentLine = word
			} else {
				currentLine += " " + word
			}
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

func formatRupiah(val int) string {
	strVal := fmt.Sprintf("%d", val)
	var result []string
	for i := len(strVal); i > 0; i -= 3 {
		start := i - 3
		if start < 0 {
			start = 0
		}
		result = append([]string{strVal[start:i]}, result...)
	}
	return "Rp " + strings.Join(result, ".")
}

func compilePreviewText(template, store, itemName string) string {
	var sb strings.Builder
	if template == "Stadium Reservation Sheets" {
		sb.WriteString("      ORANGE SPORT CENTER\n")
		sb.WriteString("    JL. KAPTEN YUSUF. BOGOR\n")
		sb.WriteString("--------------------------------\n")
		sb.WriteString("Kode : R-260519-0225\n")
		sb.WriteString("Tgl  : 2026-05-19 08:44:28\n")
		sb.WriteString("Plg  : TIMOTHY\n")
		sb.WriteString("Operator : IYONG\n")
		sb.WriteString("--------------------------------\n")
		sb.WriteString("      " + strings.ToUpper(itemName) + "\n")
		sb.WriteString("--------------------------------\n")
		sb.WriteString(formatLine("Jam", "11:00 - 17:00", 32) + "\n")
		sb.WriteString(formatLine("Tgl", "Rabu, 27/05/2026", 32) + "\n")
		sb.WriteString(formatLine("Tagihan", formatRupiah(1260000), 32) + "\n")
		sb.WriteString(formatLine("Bayar", formatRupiah(1260000), 32) + "\n")
		sb.WriteString(formatLine("Sisa", formatRupiah(0), 32) + "\n")
		sb.WriteString("================================\n")
		sb.WriteString("          Dicetak pada:\n")
		sb.WriteString(fmt.Sprintf("     %s\n", time.Now().Format("2006-01-02 15:04:05")))
		sb.WriteString("\n   Thanks for your purchase!\n")
		sb.WriteString("  Kritik & Saran : 62899911919\n")
	} else {
		sb.WriteString("          " + strings.ToUpper(store) + "\n")
		sb.WriteString(fmt.Sprintf("Date   : %s\n", time.Now().Format("2006-01-02 15:04:05")))
		sb.WriteString("Invoice: INV-POS-9812\n")
		sb.WriteString("Cashier: Adam\n")
		sb.WriteString("--------------------------------\n")
		itemLines := wrapText(itemName, 32)
		for _, line := range itemLines {
			sb.WriteString(line + "\n")
		}
		sb.WriteString(formatLine("  1 x "+formatRupiah(28000), formatRupiah(28000), 32) + "\n")
		sb.WriteString("Iced Sweet Jasmine Tea\n")
		sb.WriteString(formatLine("  2 x "+formatRupiah(6000), formatRupiah(12000), 32) + "\n")
		sb.WriteString("--------------------------------\n")
		sb.WriteString(formatLine("Subtotal", formatRupiah(40000), 32) + "\n")
		sb.WriteString(formatLine("Tax", formatRupiah(4000), 32) + "\n")
		sb.WriteString("--------------------------------\n")
		sb.WriteString(formatLine("GRAND TOTAL", formatRupiah(44000), 32) + "\n")
		sb.WriteString("--------------------------------\n")
		sb.WriteString(formatLine("Payment", "QRIS", 32) + "\n")
		sb.WriteString(formatLine("Paid", formatRupiah(44000), 32) + "\n")
		sb.WriteString(formatLine("Change", formatRupiah(0), 32) + "\n")
		sb.WriteString("--------------------------------\n")
		sb.WriteString("      POWERED BY VOLTSPOOL\n")
		sb.WriteString("    THANK YOU FOR VISITING!\n")
	}
	return sb.String()
}

// ─────────────────────────────────────────────────────────────
// UI building helpers
// ─────────────────────────────────────────────────────────────

// sectionLabel creates a small muted section header.
func sectionLabel(text string) *canvas.Text {
	t := canvas.NewText(text, colFgDim)
	t.TextSize = 10
	t.TextStyle = fyne.TextStyle{Bold: true}
	return t
}

// statusDot returns a colored circle canvas for status indicators.
func statusDot(active bool) *canvas.Circle {
	if active {
		return canvas.NewCircle(colSuccess)
	}
	return canvas.NewCircle(colDanger)
}

// pill creates a small colored badge-like label.
func pill(text string, col color.Color) *canvas.Text {
	t := canvas.NewText(text, col)
	t.TextSize = 10
	t.TextStyle = fyne.TextStyle{Bold: true}
	return t
}

// ─────────────────────────────────────────────────────────────
// Main
// ─────────────────────────────────────────────────────────────

func main() {
	myApp := app.New()
	myApp.Settings().SetTheme(&enterpriseTheme{})

	if logoRes, err := fyne.LoadResourceFromPath("app-logo.png"); err == nil {
		myApp.SetIcon(logoRes)
	}

	myWindow := myApp.NewWindow("VoltSpool — Print Operations Console")
	myWindow.Resize(fyne.NewSize(960, 740))
	myWindow.SetPadded(false)

	redirector := &LogRedirector{}
	log.SetOutput(io.MultiWriter(os.Stdout, redirector))
	log.SetFlags(0)

	// ─── Status bar (top) ───────────────────────────────────
	statusBarDot := statusDot(false)
	statusBarLabel := canvas.NewText("AGENT OFFLINE", colFgMuted)
	statusBarLabel.TextSize = 10
	statusBarLabel.TextStyle = fyne.TextStyle{Bold: true}

	versionLabel := canvas.NewText("v0.1.0", colFgMuted)
	versionLabel.TextSize = 9

	statusBar := container.NewHBox(
		statusBarDot,
		statusBarLabel,
		layout.NewSpacer(),
		versionLabel,
	)
	statusBarBg := canvas.NewRectangle(colSurface)
	statusBarContainer := container.NewStack(statusBarBg, container.NewPadded(statusBar))

	// ─── Tab 1: Dashboard ───────────────────────────────────
	bindEntry := widget.NewEntry()
	bindEntry.SetText("127.0.0.1")

	portEntry := widget.NewEntry()
	portEntry.SetText("7878")

	printerSelect := widget.NewSelect([]string{"RPP02N"}, func(s string) {
		log.Printf("Printer changed → %s", s)
	})
	printerSelect.SetSelected("RPP02N")

	apiKeyEntry := widget.NewPasswordEntry()
	apiKeyEntry.SetPlaceHolder("Set API key to protect endpoints")

	// Status labels
	printerStatusText := canvas.NewText("NOT SCANNED", colFgMuted)
	printerStatusText.TextSize = 11
	printerStatusText.TextStyle = fyne.TextStyle{Bold: true}

	printerListText := canvas.NewText("No printers detected yet", colFgMuted)
	printerListText.TextSize = 10

	sslStatusText := canvas.NewText("PENDING", colWarning)
	sslStatusText.TextSize = 11
	sslStatusText.TextStyle = fyne.TextStyle{Bold: true}

	checkSSL := func() {
		_, ec := os.Stat("cert.pem")
		_, ek := os.Stat("key.pem")
		if ec == nil && ek == nil {
			sslStatusText.Text = "ACTIVE"
			sslStatusText.Color = colSuccess
		} else {
			sslStatusText.Text = "PENDING"
			sslStatusText.Color = colWarning
		}
		sslStatusText.Refresh()
	}

	refreshPrinterStatus := func() {
		pName := printerSelect.Selected
		if pName == "" || pName == "Custom..." {
			fyne.Do(func() {
				printerStatusText.Text = "NO PRINTER"
				printerStatusText.Color = colFgMuted
				printerStatusText.Refresh()
			})
			return
		}
		engine := printer.NewPrintEngine()
		status, err := engine.GetPrinterStatus(pName)
		if err != nil {
			fyne.Do(func() {
				printerStatusText.Text = "ERROR"
				printerStatusText.Color = colDanger
				printerStatusText.Refresh()
			})
			return
		}
		fyne.Do(func() {
			printerStatusText.Text = status
			switch status {
			case "ONLINE":
				printerStatusText.Color = colSuccess
			case "PAPER_OUT", "PAPER_JAM":
				printerStatusText.Color = colWarning
			default:
				printerStatusText.Color = colDanger
			}
			printerStatusText.Refresh()
		})
	}

	refreshPrinters := func() {
		engine := printer.NewPrintEngine()
		list, err := engine.ListPrinters()
		if err != nil {
			fyne.Do(func() {
				printerListText.Text = "Failed to query system printers"
				printerListText.Color = colDanger
				printerListText.Refresh()
			})
			return
		}
		if len(list) == 0 {
			fyne.Do(func() {
				printerListText.Text = "No printers found — using default RPP02N"
				printerListText.Color = colWarning
				printerListText.Refresh()
			})
		} else {
			var names []string
			for _, p := range list {
				names = append(names, p.Name)
			}
			fyne.Do(func() {
				printerListText.Text = strings.Join(names, "  •  ")
				printerListText.Color = colFg
				printerListText.Refresh()

				options := make([]string, 0, len(names)+1)
				options = append(options, names...)
				options = append(options, "Custom...")
				printerSelect.Options = options
				if len(names) > 0 && (printerSelect.Selected == "" || printerSelect.Selected == "RPP02N" || printerSelect.Selected == "Custom...") {
					printerSelect.SetSelected(names[0])
				}
				printerSelect.Refresh()
				refreshPrinterStatus()
			})
			log.Printf("Discovered printers: %v", names)
		}
	}

	// ─── Server controls ─────────────────────────────────────
	var btnStart, btnStop *widget.Button

	updateStatusUI := func(online bool) {
		if online {
			statusBarDot.FillColor = colSuccess
			statusBarDot.Refresh()
			statusBarLabel.Text = "AGENT ONLINE"
			statusBarLabel.Color = colSuccess
			statusBarLabel.Refresh()
		} else {
			statusBarDot.FillColor = colDanger
			statusBarDot.Refresh()
			statusBarLabel.Text = "AGENT OFFLINE"
			statusBarLabel.Color = colFgMuted
			statusBarLabel.Refresh()
		}
	}

	btnStart = widget.NewButtonWithIcon("Start Agent", theme.MediaPlayIcon(), func() {
		serverMutex.Lock()
		defer serverMutex.Unlock()
		if isServerActive {
			return
		}

		os.Setenv("PRINT_AGENT_BIND", bindEntry.Text)
		os.Setenv("PRINT_AGENT_PORT", portEntry.Text)
		os.Setenv("DEFAULT_PRINTER_NAME", printerSelect.Selected)
		os.Setenv("PRINT_AGENT_KEY", apiKeyEntry.Text)

		cfg := config.LoadConfig()
		srvInstance = server.NewServer(cfg)
		isServerActive = true

		btnStart.Disable()
		btnStop.Enable()
		updateStatusUI(true)
		log.Println("Agent starting on " + cfg.BindAddr + ":" + cfg.Port)

		go func() {
			listener, err := net.Listen("tcp", net.JoinHostPort(cfg.BindAddr, cfg.Port))
			if err != nil {
				log.Printf("FATAL: port %s is locked", cfg.Port)
				guiUpdateChan <- GUIUpdate{Type: "RESET_BUTTONS"}
				return
			}
			listener.Close()
			if err := srvInstance.Start(); err != nil && err != http.ErrServerClosed {
				log.Printf("FATAL: server error: %v", err)
				guiUpdateChan <- GUIUpdate{Type: "RESET_BUTTONS"}
			}
		}()
		refreshPrinters()
	})

	btnStop = widget.NewButtonWithIcon("Stop Agent", theme.MediaStopIcon(), func() {
		serverMutex.Lock()
		defer serverMutex.Unlock()
		if !isServerActive || srvInstance == nil {
			return
		}
		log.Println("Shutting down agent...")
		go func() {
			serverMutex.Lock()
			defer serverMutex.Unlock()
			if srvInstance != nil {
				_ = srvInstance.Stop()
				srvInstance = nil
			}
			guiUpdateChan <- GUIUpdate{Type: "RESET_BUTTONS"}
		}()
	})
	btnStop.Disable()

	btnScan := widget.NewButtonWithIcon("Scan Printers", theme.ViewRefreshIcon(), refreshPrinters)

	// ─── Config form ─────────────────────────────────────────
	formGrid := container.New(layout.NewFormLayout(),
		widget.NewLabel("Bind Address"), bindEntry,
		widget.NewLabel("HTTP Port"), portEntry,
		widget.NewLabel("Target Printer"), printerSelect,
		widget.NewLabel("API Key"), apiKeyEntry,
	)

	// ─── Status cards row ────────────────────────────────────
	cardPrinterStatus := widget.NewCard("Printer", "", container.NewVBox(
		sectionLabel("HARDWARE STATUS"),
		printerStatusText,
		widget.NewSeparator(),
		sectionLabel("DETECTED QUEUES"),
		printerListText,
	))

	cardSSLStatus := widget.NewCard("TLS/SSL", "", container.NewVBox(
		sectionLabel("CERTIFICATE STATUS"),
		sslStatusText,
		widget.NewSeparator(),
		sectionLabel("HTTPS PORT"),
		canvas.NewText("7879", colFg),
	))

	statusRow := container.NewGridWithColumns(2, cardPrinterStatus, cardSSLStatus)

	// ─── Console log ─────────────────────────────────────────
	consoleLog := widget.NewMultiLineEntry()
	consoleLog.Disable()
	consoleLog.SetText("VoltSpool agent console initialized.\n")
	consoleLog.TextStyle = fyne.TextStyle{Monospace: true}

	consoleScroll := container.NewScroll(consoleLog)
	consoleScroll.SetMinSize(fyne.NewSize(0, 200))

	cardConsole := widget.NewCard("Console", "Real-time agent log output", consoleScroll)

	// ─── Assemble Tab 1 ─────────────────────────────────────
	leftPanel := container.NewVBox(
		widget.NewCard("Configuration", "Server bindings and credentials", formGrid),
		widget.NewSeparator(),
		container.NewGridWithColumns(2, btnStart, btnStop),
	)

	rightPanel := container.NewVBox(
		widget.NewCard("Printer Discovery", "Scan and select target printer", container.NewVBox(
			btnScan,
			widget.NewSeparator(),
			statusRow,
		)),
	)

	dashboardTab := container.NewBorder(
		nil, nil, nil, nil,
		container.NewVBox(
			container.NewGridWithColumns(2, leftPanel, rightPanel),
			cardConsole,
		),
	)

	// ─── Tab 2: Receipt Simulator ────────────────────────────
	templateSelect := widget.NewSelect([]string{"Retail POS Transaction", "Stadium Reservation Sheets"}, nil)
	templateSelect.SetSelected("Retail POS Transaction")

	simStoreEntry := widget.NewEntry()
	simStoreEntry.SetText("KOPI RAJIN")

	simItemEntry := widget.NewEntry()
	simItemEntry.SetText("Hazelnut Chocolate Latte Extra Shot Cold Brew")

	// Receipt preview
	emulatorBg := canvas.NewRectangle(color.RGBA{R: 2, G: 6, B: 23, A: 255})
	emulatorText := widget.NewLabel("")
	emulatorText.TextStyle = fyne.TextStyle{Monospace: true}
	emulatorTextWrapper := container.NewPadded(emulatorText)
	emulatorTape := container.NewStack(emulatorBg, emulatorTextWrapper)
	emulatorScroll := container.NewScroll(emulatorTape)
	emulatorScroll.SetMinSize(fyne.NewSize(320, 0))

	previewLabel := sectionLabel("RECEIPT PREVIEW")
	previewHeader := container.NewHBox(previewLabel, layout.NewSpacer(), pill("32 COL", colFgMuted))

	emulatorCard := widget.NewCard("", "", container.NewBorder(
		previewHeader, nil, nil, nil,
		emulatorScroll,
	))

	updateSimulatorPreview := func() {
		text := compilePreviewText(templateSelect.Selected, simStoreEntry.Text, simItemEntry.Text)
		fyne.Do(func() { emulatorText.SetText(text) })
	}

	templateSelect.OnChanged = func(s string) {
		if s == "Stadium Reservation Sheets" {
			simStoreEntry.SetText("ORANGE SPORT CENTER")
			simItemEntry.SetText("LAPANGAN FUTSAL A")
		} else {
			simStoreEntry.SetText("KOPI RAJIN")
			simItemEntry.SetText("Hazelnut Chocolate Latte Extra Shot Cold Brew")
		}
		updateSimulatorPreview()
	}
	simStoreEntry.OnChanged = func(s string) { updateSimulatorPreview() }
	simItemEntry.OnChanged = func(s string) { updateSimulatorPreview() }

	pdfModeCheck := widget.NewCheck("Save as PDF instead of printing", nil)

	btnSimPrint := widget.NewButtonWithIcon("Compile & Print", theme.DocumentPrintIcon(), func() {
		pName := printerSelect.Selected
		if pName == "" || pName == "Custom..." {
			log.Printf("No printer selected")
			return
		}
		tmpl := templateSelect.Selected
		store := simStoreEntry.Text
		item := simItemEntry.Text

		log.Printf("Compiling receipt → %s", pName)

		go func() {
			var rawBytes []byte
			var err error
			previewText := compilePreviewText(tmpl, store, item)

			if tmpl == "Stadium Reservation Sheets" {
				rawBytes, err = escpos.GenerateGenericReceipt(escpos.GenericReceipt{
					PrinterName: pName, Width: 32,
					Lines: []escpos.GenericLine{
						{Type: "logo"},
						{Type: "text", Text: store, Align: "CENTER", Bold: true, Size: "DOUBLE_HEIGHT"},
						{Type: "text", Text: "JL. KAPTEN YUSUF. BOGOR", Align: "CENTER"},
						{Type: "feed", Lines: 1},
						{Type: "text", Text: "Kode : R-260519-0225"},
						{Type: "text", Text: "Tgl  : 2026-05-19 08:44:28"},
						{Type: "text", Text: "Plg  : TIMOTHY"},
						{Type: "text", Text: "Operator : IYONG"},
						{Type: "divider", Char: "-"},
						{Type: "text", Text: item, Align: "CENTER", Bold: true},
						{Type: "divider", Char: "-"},
						{Type: "columns", Left: "Jam", Right: "11:00 - 17:00"},
						{Type: "columns", Left: "Tgl", Right: "Rabu, 27/05/2026"},
						{Type: "columns", Left: "Tagihan", Right: formatRupiah(1260000)},
						{Type: "columns", Left: "Bayar", Right: formatRupiah(1260000)},
						{Type: "columns", Left: "Sisa", Right: "0"},
						{Type: "divider", Char: "="},
						{Type: "text", Text: "Dicetak pada:", Align: "CENTER"},
						{Type: "text", Text: time.Now().Format("2006-01-02 15:04:05"), Align: "CENTER"},
						{Type: "feed", Lines: 1},
						{Type: "text", Text: "Thanks for your purchase!", Align: "CENTER"},
						{Type: "text", Text: "Kritik & Saran : 62899911919", Align: "CENTER"},
						{Type: "feed", Lines: 2},
						{Type: "cut"},
					},
				})
			} else {
				rawBytes, err = escpos.GenerateESCPOSText(escpos.Receipt{
					PrinterName: pName, StoreName: store, Cashier: "Adam",
					InvoiceNo: "INV-" + fmt.Sprintf("%d", time.Now().Unix()%10000),
					Items: []escpos.Item{
						{Name: item, Qty: 1, Price: 28000, Total: 28000},
						{Name: "Iced Sweet Jasmine Tea", Qty: 2, Price: 6000, Total: 12000},
					},
					Subtotal: 40000, Tax: 4000, GrandTotal: 44000,
					PaymentMethod: "QRIS", Paid: 44000,
					Footer: "POWERED BY VOLTSPOOL\nTHANK YOU!", KickDrawer: true,
				})
			}

			if err != nil {
				log.Printf("Compile error: %v", err)
				return
			}

			if pdfModeCheck.Checked {
				pdfPath := pdfprinter.DefaultOutputPath()
				if err := pdfprinter.GenerateReceiptPDF(previewText, pdfPath); err != nil {
					log.Printf("PDF error: %v", err)
					return
				}
				log.Printf("PDF saved → %s", pdfPath)
				abs, _ := filepath.Abs(pdfPath)
				switch runtime.GOOS {
				case "darwin":
					exec.Command("open", abs).Start()
				case "windows":
					exec.Command("cmd", "/c", "start", abs).Start()
				default:
					exec.Command("xdg-open", abs).Start()
				}
				return
			}

			engine := printer.NewPrintEngine()
			if err := engine.PrintRaw(pName, rawBytes); err != nil {
				log.Printf("Print error: %v", err)
			} else {
				log.Printf("Printed successfully → %s", pName)
			}
		}()
	})

	simForm := widget.NewCard("Template", "Configure receipt parameters", container.NewVBox(
		sectionLabel("TEMPLATE TYPE"),
		templateSelect,
		sectionLabel("STORE NAME"),
		simStoreEntry,
		sectionLabel("ITEM DESCRIPTION"),
		simItemEntry,
		widget.NewSeparator(),
		pdfModeCheck,
		widget.NewSeparator(),
		btnSimPrint,
	))

	simulatorTab := container.NewGridWithColumns(2, simForm, emulatorCard)

	// ─── Tabs ────────────────────────────────────────────────
	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Dashboard", theme.HomeIcon(), dashboardTab),
		container.NewTabItemWithIcon("Simulator", theme.DocumentPrintIcon(), simulatorTab),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	// ─── Root layout: status bar + tabs ──────────────────────
	root := container.NewBorder(statusBarContainer, nil, nil, nil, tabs)
	myWindow.SetContent(root)

	// ─── Bootstrap ───────────────────────────────────────────
	refreshPrinters()
	updateSimulatorPreview()
	checkSSL()

	// Printer selector → Custom dialog
	printerSelect.OnChanged = func(s string) {
		if s == "Custom..." {
			dw := myApp.NewWindow("Add Custom Printer")
			ce := widget.NewEntry()
			ce.SetPlaceHolder("Enter printer queue name...")
			dw.SetContent(container.NewVBox(
				widget.NewLabel("Enter the exact OS printer name:"),
				ce,
				container.NewHBox(
					widget.NewButton("Cancel", func() {
						if len(printerSelect.Options) > 1 {
							printerSelect.SetSelected(printerSelect.Options[0])
						} else {
							printerSelect.SetSelected("RPP02N")
						}
						dw.Close()
					}),
					widget.NewButton("Confirm", func() {
						val := strings.TrimSpace(ce.Text)
						if val == "" {
							return
						}
						newOpts := make([]string, 0, len(printerSelect.Options)+1)
						for _, o := range printerSelect.Options {
							if o != "Custom..." {
								newOpts = append(newOpts, o)
							}
						}
						newOpts = append(newOpts, val, "Custom...")
						printerSelect.Options = newOpts
						printerSelect.SetSelected(val)
						printerSelect.Refresh()
						log.Printf("Custom printer added: %s", val)
						dw.Close()
					}),
				),
			))
			dw.Resize(fyne.NewSize(380, 140))
			dw.Show()
		} else {
			refreshPrinterStatus()
		}
	}

	// ─── Background polling loop ─────────────────────────────
	go func() {
		ticks := 0
		for {
			time.Sleep(200 * time.Millisecond)
			ticks++
			if ticks >= 20 {
				ticks = 0
				refreshPrinterStatus()
				fyne.Do(func() { checkSSL() })
			}

			redirector.mu.Lock()
			if len(redirector.logs) > 0 {
				newLogs := strings.Join(redirector.logs, "")
				redirector.logs = nil
				redirector.mu.Unlock()
				fyne.Do(func() {
					cur := consoleLog.Text + newLogs
					if len(cur) > 15000 {
						cur = "--- log truncated ---\n" + cur[len(cur)-7500:]
					}
					consoleLog.SetText(cur)
				})
			} else {
				redirector.mu.Unlock()
			}

			select {
			case update := <-guiUpdateChan:
				if update.Type == "RESET_BUTTONS" {
					serverMutex.Lock()
					isServerActive = false
					serverMutex.Unlock()
					fyne.Do(func() {
						btnStart.Enable()
						btnStop.Disable()
						updateStatusUI(false)
					})
				}
			default:
			}
		}
	}()

	// ─── System tray ─────────────────────────────────────────
	myWindow.SetCloseIntercept(func() {
		myWindow.Hide()
		log.Println("Window hidden — agent continues in tray")
	})

	if desk, ok := myApp.(desktop.App); ok {
		desk.SetSystemTrayMenu(fyne.NewMenu("VoltSpool",
			fyne.NewMenuItem("Show Console", func() { myWindow.Show() }),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Stop & Exit", func() {
				serverMutex.Lock()
				if isServerActive && srvInstance != nil {
					_ = srvInstance.Stop()
				}
				serverMutex.Unlock()
				myApp.Quit()
			}),
		))
	}

	myWindow.ShowAndRun()
}
