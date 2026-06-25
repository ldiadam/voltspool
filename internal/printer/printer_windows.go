//go:build windows

package printer

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// windowsEngine implements the PrintEngine interface for Windows.
type windowsEngine struct{}

func getPlatformEngine() PrintEngine {
	return &windowsEngine{}
}

// DOC_INFO_1 describes a document that will be printed.
type DOC_INFO_1 struct {
	DocName    *uint16
	OutputFile *uint16
	Datatype   *uint16
}

var (
	winspool     = syscall.NewLazyDLL("winspool.drv")
	openPrinter  = winspool.NewProc("OpenPrinterW")
	closePrinter = winspool.NewProc("ClosePrinter")
	startDoc     = winspool.NewProc("StartDocPrinterW")
	endDoc       = winspool.NewProc("EndDocPrinter")
	startPage    = winspool.NewProc("StartPagePrinter")
	endPage      = winspool.NewProc("EndPagePrinter")
	writePrinter = winspool.NewProc("WritePrinter")
)

// PrintRaw prints raw ESC/POS bytes on Windows by writing directly to the Windows Spooler API.
func (e *windowsEngine) PrintRaw(printerName string, data []byte) error {
	if len(data) == 0 {
		return nil
	}

	pName, err := syscall.UTF16PtrFromString(printerName)
	if err != nil {
		return fmt.Errorf("invalid printer name: %w", err)
	}

	var hPrinter syscall.Handle
	r1, _, err := openPrinter.Call(
		uintptr(unsafe.Pointer(pName)),
		uintptr(unsafe.Pointer(&hPrinter)),
		0,
	)
	if r1 == 0 {
		return fmt.Errorf("failed to open printer %q: %w", printerName, err)
	}
	defer closePrinter.Call(uintptr(hPrinter))

	pDocName, _ := syscall.UTF16PtrFromString("POS Local Print Job")
	pDataType, _ := syscall.UTF16PtrFromString("RAW")

	docInfo := DOC_INFO_1{
		DocName:    pDocName,
		OutputFile: nil,
		Datatype:   pDataType,
	}

	r1, _, err = startDoc.Call(
		uintptr(hPrinter),
		1, // Level 1 info
		uintptr(unsafe.Pointer(&docInfo)),
	)
	if r1 == 0 {
		return fmt.Errorf("failed to start document printer: %w", err)
	}
	defer endDoc.Call(uintptr(hPrinter))

	r1, _, err = startPage.Call(uintptr(hPrinter))
	if r1 == 0 {
		return fmt.Errorf("failed to start page printer: %w", err)
	}
	defer endPage.Call(uintptr(hPrinter))

	var written uint32
	r1, _, err = writePrinter.Call(
		uintptr(hPrinter),
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&written)),
	)
	if r1 == 0 {
		return fmt.Errorf("failed to write print raw data: %w", err)
	}

	return nil
}

// ListPrinters detects installed printers on Windows by executing a powershell query, with a wmic fallback.
func (e *windowsEngine) ListPrinters() ([]PrinterInfo, error) {
	// Query installed printers list using PowerShell
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Get-CimInstance Win32_Printer | Select-Object -ExpandProperty Name")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Fallback to older wmic query if PowerShell is unavailable
		cmdFallback := exec.Command("cmd", "/c", "wmic printer get name")
		var stdoutFallback bytes.Buffer
		cmdFallback.Stdout = &stdoutFallback
		if errFallback := cmdFallback.Run(); errFallback != nil {
			return []PrinterInfo{}, nil
		}

		var printers []PrinterInfo
		lines := strings.Split(stdoutFallback.String(), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.EqualFold(line, "Name") {
				continue
			}
			printers = append(printers, PrinterInfo{Name: line})
		}
		return printers, nil
	}

	var printers []PrinterInfo
	lines := strings.Split(stdout.String(), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			printers = append(printers, PrinterInfo{Name: line})
		}
	}

	return printers, nil
}

// PRINTER_INFO_6 defines printer status level 6 information.
type PRINTER_INFO_6 struct {
	DwStatus uint32
}

var getPrinter = winspool.NewProc("GetPrinterW")

// GetPrinterStatus queries the hardware/queue status of a printer on Windows natively using winspool.drv.
func (e *windowsEngine) GetPrinterStatus(printerName string) (string, error) {
	if printerName == "" {
		return "OFFLINE", fmt.Errorf("printer name cannot be empty")
	}

	pName, err := syscall.UTF16PtrFromString(printerName)
	if err != nil {
		return "OFFLINE", fmt.Errorf("invalid printer name: %w", err)
	}

	var hPrinter syscall.Handle
	r1, _, err := openPrinter.Call(
		uintptr(unsafe.Pointer(pName)),
		uintptr(unsafe.Pointer(&hPrinter)),
		0,
	)
	if r1 == 0 {
		return "NOT_FOUND", nil
	}
	defer closePrinter.Call(uintptr(hPrinter))

	var needed uint32
	// Preflight call to retrieve required buffer size for Level 6 info
	getPrinter.Call(
		uintptr(hPrinter),
		6, // Level 6
		0,
		0,
		uintptr(unsafe.Pointer(&needed)),
	)

	if needed == 0 {
		return "OFFLINE", nil
	}

	buf := make([]byte, needed)
	r1, _, err = getPrinter.Call(
		uintptr(hPrinter),
		6, // Level 6
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&needed)),
	)
	if r1 == 0 {
		return "OFFLINE", fmt.Errorf("GetPrinterW Level 6 failed: %w", err)
	}

	info := (*PRINTER_INFO_6)(unsafe.Pointer(&buf[0]))
	status := info.DwStatus

	// Inspect Spooler status flags
	if status&0x00000010 != 0 { // PRINTER_STATUS_PAPER_OUT
		return "PAPER_OUT", nil
	}
	if status&0x00400000 != 0 { // PRINTER_STATUS_DOOR_OPEN (Cover/Lid open)
		return "LID_OPEN", nil
	}
	if status&0x00000080 != 0 { // PRINTER_STATUS_OFFLINE
		return "OFFLINE", nil
	}
	if status&0x00000008 != 0 { // PRINTER_STATUS_PAPER_JAM
		return "PAPER_JAM", nil
	}
	if status&0x00000001 != 0 { // PRINTER_STATUS_PAUSED
		return "PAUSED", nil
	}
	if status&0x00000200 != 0 { // PRINTER_STATUS_BUSY
		return "BUSY", nil
	}

	return "ONLINE", nil
}

