//go:build !windows

package printer

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// cupsEngine implements the PrintEngine interface for macOS/Linux using CUPS.
type cupsEngine struct{}

func getPlatformEngine() PrintEngine {
	return &cupsEngine{}
}

// PrintRaw prints raw ESC/POS bytes on macOS/Linux using CUPS command-line `lp`
func (e *cupsEngine) PrintRaw(printerName string, data []byte) error {
	if printerName == "" {
		return fmt.Errorf("printer name cannot be empty")
	}

	// -o raw option sends the bytes directly without CUPS rendering filters
	cmd := exec.Command("lp", "-d", printerName, "-o", "raw")
	cmd.Stdin = bytes.NewReader(data)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run 'lp' command: %v, stderr: %q", err, stderr.String())
	}
	return nil
}

// ListPrinters detects installed printers on macOS/Linux using `lpstat -a`
func (e *cupsEngine) ListPrinters() ([]PrinterInfo, error) {
	cmd := exec.Command("lpstat", "-a")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// If CUPS is not running or command is not found, return empty slice rather than crashing
		return []PrinterInfo{}, nil
	}

	var printers []PrinterInfo
	lines := strings.Split(stdout.String(), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "lpstat:") {
			continue
		}

		// The printer name is the first word on each line (e.g. "RPP02N accepting requests since...")
		fields := strings.Fields(line)
		if len(fields) > 0 {
			printerName := strings.TrimSuffix(fields[0], ":")
			printers = append(printers, PrinterInfo{Name: printerName})
		}
	}

	return printers, nil
}

// GetPrinterStatus queries the status of the specified printer on macOS/Linux by parsing lpstat -p.
func (e *cupsEngine) GetPrinterStatus(printerName string) (string, error) {
	if printerName == "" {
		return "OFFLINE", fmt.Errorf("printer name cannot be empty")
	}

	cmd := exec.Command("lpstat", "-p", printerName)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errStr := strings.ToLower(stderr.String())
		if strings.Contains(errStr, "does not exist") || strings.Contains(errStr, "invalid") {
			return "NOT_FOUND", nil
		}
		// If lpstat fails for other reasons, assume it is offline or unreachable
		return "OFFLINE", nil
	}

	outStr := strings.ToLower(stdout.String())
	if strings.Contains(outStr, "paper out") || strings.Contains(outStr, "media out") {
		return "PAPER_OUT", nil
	}
	if strings.Contains(outStr, "door open") || strings.Contains(outStr, "cover open") || strings.Contains(outStr, "lid open") {
		return "LID_OPEN", nil
	}
	if strings.Contains(outStr, "paused") || strings.Contains(outStr, "disabled") {
		return "PAUSED", nil
	}
	if strings.Contains(outStr, "idle") || strings.Contains(outStr, "printing") {
		return "ONLINE", nil
	}

	return "ONLINE", nil
}

