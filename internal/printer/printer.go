package printer

// PrinterInfo represents details of an installed printer.
type PrinterInfo struct {
	Name string `json:"name"`
}

// PrintEngine defines the interface for local printer detection and raw printing.
type PrintEngine interface {
	// PrintRaw sends raw bytes (such as ESC/POS payloads) directly to the specified printer queue.
	PrintRaw(printerName string, data []byte) error

	// ListPrinters returns a list of installed local printers on the operating system.
	ListPrinters() ([]PrinterInfo, error)

	// GetPrinterStatus queries the status of the specified printer (e.g. "ONLINE", "PAPER_OUT", "OFFLINE").
	GetPrinterStatus(printerName string) (string, error)
}

// NewPrintEngine returns the platform-appropriate raw printing engine.
// Since the concrete types have different build constraints, the actual instantiation
// is handled by the platform-specific factory files.
func NewPrintEngine() PrintEngine {
	return getPlatformEngine()
}
