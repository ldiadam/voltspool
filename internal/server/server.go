package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"print-agent/internal/config"
	"print-agent/internal/escpos"
	"print-agent/internal/printer"
	"time"
)

// Server encapsulates the HTTP API routing and print driver.
type Server struct {
	config   *config.Config
	engine   printer.PrintEngine
	httpSrv  *http.Server
	httpsSrv *http.Server
}

// NewServer creates a new instance of the Server with target configuration.
func NewServer(cfg *config.Config) *Server {
	return &Server{
		config: cfg,
		engine: printer.NewPrintEngine(),
	}
}

// Start launches the dual HTTP & HTTPS print servers in parallel.
func (s *Server) Start() error {
	// 1. Ensure SSL/TLS self-signed certificates exist
	certFile, keyFile, err := EnsureCertificatesExist()
	if err != nil {
		log.Printf("[SSL] Warning: Failed to generate local self-signed SSL/TLS certificates: %v. Running in HTTP-only mode.", err)
	}

	httpAddr := net.JoinHostPort(s.config.BindAddr, s.config.Port)
	httpsAddr := net.JoinHostPort(s.config.BindAddr, s.config.HttpsPort)
	
	mux := http.NewServeMux()

	// Endpoints with middlewares wrapped
	mux.HandleFunc("/", s.allowAll(s.handleRoot))
	mux.HandleFunc("/health", s.allowAll(s.handleHealth))
	mux.HandleFunc("/ticket", s.allowAll(s.handleTicket))
	mux.HandleFunc("/logo", s.allowAll(s.auth(s.handleLogoUpload)))
	mux.HandleFunc("/logo.png", s.handleLogoImage)
	mux.HandleFunc("/printers", s.allowAll(s.auth(s.handlePrinters)))
	mux.HandleFunc("/printers/status", s.allowAll(s.auth(s.handlePrinterStatus)))
	mux.HandleFunc("/print", s.allowAll(s.auth(s.handlePrint)))
	mux.HandleFunc("/print-generic", s.allowAll(s.auth(s.handlePrintGeneric)))
	mux.HandleFunc("/print-raw", s.allowAll(s.auth(s.handlePrintRaw)))
	mux.HandleFunc("/print-task", s.allowAll(s.auth(s.handlePrintTask)))
	mux.HandleFunc("/test-print", s.allowAll(s.auth(s.handleTestPrint)))

	log.Printf("[SERVER] Default Printer fallback: %s", s.config.DefaultPrinterName)
	if s.config.PrintAgentKey != "" {
		log.Println("[SERVER] Security: X-Print-Agent-Key authentication is ENABLED")
	} else {
		log.Println("[SERVER] Security: Token authentication is DISABLED (Key is empty)")
	}

	// Configure HTTP listener
	s.httpSrv = &http.Server{
		Addr:    httpAddr,
		Handler: mux,
	}

	// Configure HTTPS listener (if certificate files are successfully located/created)
	if certFile != "" && keyFile != "" {
		s.httpsSrv = &http.Server{
			Addr:    httpsAddr,
			Handler: mux,
		}

		// Launch the secure HTTPS server in a background goroutine
		go func() {
			log.Printf("[SERVER] Secure HTTPS Local Print Agent starting on https://%s", httpsAddr)
			if err := s.httpsSrv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
				log.Printf("[FATAL] HTTPS Server encountered critical error: %v", err)
			}
		}()
	}

	// Block on the standard HTTP server
	log.Printf("[SERVER] Local Print Agent starting on http://%s", httpAddr)
	return s.httpSrv.ListenAndServe()
}

// Stop shuts down the running HTTP and HTTPS servers gracefully.
func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var httpErr, httpsErr error

	if s.httpSrv != nil {
		httpErr = s.httpSrv.Shutdown(ctx)
	}

	if s.httpsSrv != nil {
		httpsErr = s.httpsSrv.Shutdown(ctx)
	}

	if httpErr != nil {
		return httpErr
	}
	return httpsErr
}

// allowAll is a middleware that sets CORS headers to allow all origins.
// Since this agent binds to 127.0.0.1 only, external sites can only reach it
// from the user's own browser. The API key auth protects sensitive endpoints.
func (s *Server) allowAll(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Print-Agent-Key")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

// auth is a middleware that validates the API Key header, if configured.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.config.PrintAgentKey != "" {
			key := r.Header.Get("X-Print-Agent-Key")
			if key != s.config.PrintAgentKey {
				log.Printf("[AUTH] Blocked unauthorized request to %s from %s", r.URL.Path, r.RemoteAddr)
				s.writeJSONError(w, http.StatusUnauthorized, "Invalid or missing X-Print-Agent-Key header")
				return
			}
		}
		next(w, r)
	}
}

// handleHealth returns a simple OK check response.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// handlePrinters returns lists of available local OS printer names.
func (s *Server) handlePrinters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	printers, err := s.engine.ListPrinters()
	if err != nil {
		log.Printf("[PRINTERS] Error enumerating printers: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list printers: %v", err))
		return
	}

	s.writeJSON(w, http.StatusOK, printers)
}

// handlePrinterStatus checks the current status of the printer and returns a JSON response.
func (s *Server) handlePrinterStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	printerName := r.URL.Query().Get("name")
	if printerName == "" {
		printerName = r.URL.Query().Get("printer")
	}
	if printerName == "" {
		printerName = s.config.DefaultPrinterName
	}

	status, err := s.engine.GetPrinterStatus(printerName)
	if err != nil {
		log.Printf("[STATUS] Error querying status for printer %q: %v", printerName, err)
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"printerName": printerName,
			"status":      "OFFLINE",
			"error":       err.Error(),
		})
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"printerName": printerName,
		"status":      status,
	})
}

// handlePrint receives formatted receipt details, builds ESC/POS, and writes to spooler.
func (s *Server) handlePrint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var rec escpos.Receipt
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON request body: %v", err))
		return
	}

	// Resolve printer name
	printerName := rec.PrinterName
	if printerName == "" {
		printerName = s.config.DefaultPrinterName
	}

	log.Printf("[PRINT] Processing receipt print job for invoice %q on printer %q", rec.InvoiceNo, printerName)

	// Format structured receipt data to raw ESC/POS bytes
	rawBytes, err := escpos.GenerateESCPOSText(rec)
	if err != nil {
		log.Printf("[PRINT] Error generating ESC/POS: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to format receipt: %v", err))
		return
	}

	// Send raw payload to printing engine
	err = s.engine.PrintRaw(printerName, rawBytes)
	if err != nil {
		log.Printf("[PRINT] Error writing raw to print spooler: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to print: %v", err))
		return
	}

	log.Printf("[PRINT] Successfully sent print job for invoice %q to printer %q", rec.InvoiceNo, printerName)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Job sent to printer %s successfully", printerName),
	})
}

// handlePrintGeneric receives a custom line-by-line receipt structure, formats ESC/POS, and writes to spooler.
func (s *Server) handlePrintGeneric(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var rec escpos.GenericReceipt
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON request body: %v", err))
		return
	}

	// Resolve printer name
	printerName := rec.PrinterName
	if printerName == "" {
		printerName = s.config.DefaultPrinterName
	}

	log.Printf("[GENERIC-PRINT] Processing generic custom lines print job on printer %q", printerName)

	rawBytes, err := escpos.GenerateGenericReceipt(rec)
	if err != nil {
		log.Printf("[GENERIC-PRINT] Error compiling layout: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to format receipt: %v", err))
		return
	}

	err = s.engine.PrintRaw(printerName, rawBytes)
	if err != nil {
		log.Printf("[GENERIC-PRINT] Spooler print error: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to print: %v", err))
		return
	}

	log.Printf("[GENERIC-PRINT] Job sent to printer %q successfully", printerName)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Generic print job sent to printer %s successfully", printerName),
	})
}

// handleTestPrint schedules a mock dummy receipt print to check print styles.
func (s *Server) handleTestPrint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Read optional printerName override from body
	var req struct {
		PrinterName string `json:"printerName"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	printerName := req.PrinterName
	if printerName == "" {
		printerName = s.config.DefaultPrinterName
	}

	log.Printf("[TEST-PRINT] Request received for printer: %q", printerName)

	testReceipt := escpos.Receipt{
		PrinterName:   printerName,
		StoreName:     "TEST KIOSK",
		Cashier:       "Operator",
		InvoiceNo:     "INV-TEST-0001",
		Items: []escpos.Item{
			{Name: "Signature Fried Rice Special Extra Hot", Qty: 1, Price: 28000, Total: 28000},
			{Name: "Iced Sweet Jasmine Tea", Qty: 2, Price: 6000, Total: 12000},
			{Name: "Crispy Crackers Bag", Qty: 3, Price: 3000, Total: 9000},
		},
		Subtotal:      49000,
		Discount:      4000,
		Tax:           4500,
		GrandTotal:    49500,
		PaymentMethod: "QRIS",
		Paid:          49500,
		Change:        0,
		Footer:        "POWERED BY GO PRINT AGENT\nTHANK YOU FOR SHOPPING!",
		KickDrawer:    true,
	}

	rawBytes, err := escpos.GenerateESCPOSText(testReceipt)
	if err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to format test receipt: %v", err))
		return
	}

	err = s.engine.PrintRaw(printerName, rawBytes)
	if err != nil {
		log.Printf("[TEST-PRINT] Printing failed: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to print: %v", err))
		return
	}

	log.Printf("[TEST-PRINT] Test print successfully printed on %q", printerName)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Test print sent to %s successfully", printerName),
	})
}

// writeJSON is a utility that writes a JSON response with status.
func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[SERVER] JSON encode error: %v", err)
	}
}

// writeJSONError is a utility that writes a standardized JSON error response.
func (s *Server) writeJSONError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]interface{}{
		"error": message,
	})
}

// handleLogoImage serves the application brand logo.
func (s *Server) handleLogoImage(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "app-logo.png")
}

// handleRoot serves the daemon status message.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.writeJSONError(w, http.StatusNotFound, "Not found")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "VoltSpool Local Secure Print Agent Daemon is ACTIVE",
		"service": "Telemetry and dashboards are consolidated into the native GUI app",
	})
}

// handleTicket serves the workforce daemon status message.
func (s *Server) handleTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "VoltSpool Workforce Dispatch Daemon is ACTIVE",
		"service": "Operate the Active Dispatch Board directly inside the native GUI app",
	})
}

// handleLogoUpload receives a PNG/JPEG file, compiles it to monochrome bit-image, and caches it as logo.bin.
func (s *Server) handleLogoUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// 1. Limit upload size to 8MB max
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse multipart form: %v", err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Missing 'file' field in multipart form upload")
		return
	}
	defer file.Close()

	log.Printf("[LOGO] Upload received: %q (Size: %d bytes)", header.Filename, header.Size)

	// 2. Decode and compile image to ESC/POS monochrome bytes
	compiledBytes, err := escpos.CompileLogo(file)
	if err != nil {
		log.Printf("[LOGO] Compilation error: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to compile image to ESC/POS: %v", err))
		return
	}

	// 3. Cache it on the local disk as logo.bin
	if err := os.WriteFile("logo.bin", compiledBytes, 0644); err != nil {
		log.Printf("[LOGO] Write error: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to cache compiled logo: %v", err))
		return
	}

	log.Printf("[LOGO] Image successfully rasterized and cached to logo.bin (compiled size: %d bytes)", len(compiledBytes))
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Store logo successfully compiled and cached as logo.bin on server!",
	})
}


// handlePrintRaw receives base64-encoded raw ESC/POS commands, decodes them, and prints them.
func (s *Server) handlePrintRaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		PrinterName string `json:"printerName"`
		Base64Data  string `json:"base64Data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON request body: %v", err))
		return
	}

	if req.Base64Data == "" {
		s.writeJSONError(w, http.StatusBadRequest, "base64Data cannot be empty")
		return
	}

	rawBytes, err := base64.StdEncoding.DecodeString(req.Base64Data)
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to decode base64Data: %v", err))
		return
	}

	printerName := req.PrinterName
	if printerName == "" {
		printerName = s.config.DefaultPrinterName
	}

	log.Printf("[RAW-PRINT] Sending %d raw bytes directly to printer %q", len(rawBytes), printerName)

	err = s.engine.PrintRaw(printerName, rawBytes)
	if err != nil {
		log.Printf("[RAW-PRINT] Error writing to spooler: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to print: %v", err))
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Raw bytes printed on %s successfully", printerName),
	})
}



// handlePrintTask receives task ticket info, compiles ESC/POS, and writes to spooler.
func (s *Server) handlePrintTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var task escpos.TaskTicket
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON request body: %v", err))
		return
	}

	// Resolve printer name
	printerName := task.PrinterName
	if printerName == "" {
		printerName = s.config.DefaultPrinterName
	}

	log.Printf("[TASK-PRINT] Processing task ticket #%q for assignee %q on printer %q", task.TaskID, task.Assignee, printerName)

	rawBytes, err := escpos.GenerateTaskTicket(task)
	if err != nil {
		log.Printf("[TASK-PRINT] Error generating ESC/POS: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to format task ticket: %v", err))
		return
	}

	err = s.engine.PrintRaw(printerName, rawBytes)
	if err != nil {
		log.Printf("[TASK-PRINT] Error writing raw to print spooler: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to print: %v", err))
		return
	}

	log.Printf("[TASK-PRINT] Successfully printed task ticket #%q on printer %q", task.TaskID, printerName)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Task ticket #%s sent to printer successfully", task.TaskID),
	})
}


