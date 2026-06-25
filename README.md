# VoltSpool — Native GUI Print Agent for 58mm ESC/POS Thermal Printer (RPP02N)

A native desktop GUI application for driving 58mm ESC/POS thermal receipt printers. Built with Go and Fyne v2, VoltSpool provides a full-featured operations console with a built-in HTTP print server, live receipt simulator, and PDF output mode — all in one polished native window.

---

## Features

- **Direct Raw Printing**: Bypasses browser printing. Sends raw ESC/POS byte commands directly to the printer hardware queue.
- **Cross-Platform Support**:
  - **macOS/Linux**: Interfaces directly with **CUPS** using raw stream pipes.
  - **Windows**: Built-in native hooks to **`winspool.drv`** system spooler APIs (CGO-free compilation).
- **Custom ESC/POS Formatter**: Formats receipts beautifully for 58mm printers (32 characters per line limit).
  - Left-aligned items, right-aligned price totals.
  - Automatic line wrapping for long item descriptions.
  - Format monetary numbers in Indonesian Rupiah style (`Rp 10.000`).
  - Cash drawer triggers (`kickDrawer`) and partial paper-cutting indicators.
- **Enhanced Security**:
  - Automatically binds to `127.0.0.1` (localhost only) to restrict external network intrusion.
  - Binds to `127.0.0.1` (localhost only) — no external network exposure.
  - Optional secure token header authentication via `X-Print-Agent-Key`.

---

## Project Structure

```text
├── cmd
│   └── print-agent-gui
│       └── main.go              # Native Go Fyne GUI desktop controller window
├── internal
│   ├── config
│   │   └── config.go            # Ports, printer settings, API key parser
│   ├── escpos
│   │   └── escpos.go            # Standard ESC/POS commands & 58mm layout processor
│   ├── printer
│   │   ├── printer.go           # High-level hardware interface definitions
│   │   ├── printer_darwin.go    # CUPS-based macOS/Linux raw driver
│   │   └── printer_windows.go   # DLL spooler-based Windows raw driver
│   └── server
│       ├── server.go            # Router, Auth check, REST controller handlers
│       └── ssl.go               # Auto-generated self-signed CA & server certificates
├── go.mod                       # Go module description
└── README.md                    # Integration guide & developer manual
```

---

## Served Web Dashboards

The Print Agent hosts a built-in dashboard directly on its HTTP server:

**POS Simulator Dashboard (`GET /`)**: Served directly at the server's root. It features a live 58mm mock monospaced thermal receipt visualizer where you can customize layouts and print instant tickets directly.

---

## Configuration

Configure the agent using the following environment variables. If left undefined, safe defaults will automatically be chosen:

| Variable | Default Value | Description |
| :--- | :--- | :--- |
| `PRINT_AGENT_BIND` | `127.0.0.1` | Host address to bind the server to. |
| `PRINT_AGENT_PORT` | `7878` | Listening port for the HTTP REST service. |
| `DEFAULT_PRINTER_NAME` | `RPP02N` | Fallback printer name if the request payload does not provide `printerName`. |
| `PRINT_AGENT_KEY` | *(None)* | Optional security key. If set, clients must send header `X-Print-Agent-Key`. |

---

## Installation & Build Commands

Ensure you have **Go 1.23 or later** installed.

*Note: Fyne requires CGO and graphics libraries on the compilation host system. Build on the target OS (macOS for Mac, Windows for Windows).*

#### Get Fyne Dependencies:
```bash
go get fyne.io/fyne/v2
go mod tidy
```

#### Run in Dev Mode:
```bash
go run ./cmd/print-agent-gui
```

#### Compile Standalone Native GUI App:
```bash
# Build for macOS (produces native Mac app)
go build -o voltspool ./cmd/print-agent-gui

# Build on Windows for Windows (produces voltspool.exe)
go build -o voltspool.exe ./cmd/print-agent-gui
```

---

## Release

Releases are automated via GitHub Actions. To create a new release:

```bash
git tag v1.0.0
git push origin v1.0.0
```

This triggers the `release.yml` workflow which builds binaries for:
- **macOS** (ARM64 + Intel)
- **Windows** (AMD64)
- **Linux** (AMD64 + ARM64)

Binaries are attached to the GitHub Release automatically.

---

## Operating System Setup

To ensure raw printing functions correctly, the RPP02N must be registered on the host system:

### 1. macOS / Linux (CUPS Setup)
- Register your thermal printer via **System Preferences -> Printers & Scanners** or using the **CUPS Web Interface** (`http://localhost:631`).
- Obtain the printer's exact queue identifier (e.g. `RPP02N`, `XP-58`, or `Thermal_Printer`).
- Test printer list using CLI:
  ```bash
  lpstat -a
  ```
- Send a test raw stream manually:
  ```bash
  echo -e "\x1b\x40Hello World\n\n\n\n\x1d\x56\x42\x00" | lp -d RPP02N -o raw
  ```

### 2. Windows Setup
- Install the manufacturer's printer driver or configure a Generic / Text-Only driver mapped to the USB port.
- Make sure the printer name matches in Windows Settings (e.g. `"RPP02N"`).
- Our Windows driver utilizes the spooler engine directly in RAW mode. Ensure that other apps are not lock-blocking the printer's USB connection when firing requests.

---

## REST Endpoints & Web Integration Examples

The service exposes four endpoints. Below are integration snippets written in Javascript:

### 1. Health Status (`GET /health`)
Verify that the service is running on the local system.

```javascript
fetch("http://localhost:7878/health")
  .then(res => res.json())
  .then(data => console.log("Print Agent Health:", data)) // Output: { ok: true }
  .catch(err => console.error("Agent is not running:", err));
```

### 2. List Local Printers (`GET /printers`)
Queries the OS spooler to list available printer queues:

```javascript
fetch("http://localhost:7878/printers", {
  headers: {
    "X-Print-Agent-Key": "your-secret-key-if-configured"
  }
})
  .then(res => res.json())
  .then(printers => console.log("Installed Printers:", printers)) // Output: [{"name":"RPP02N"}, ...]
  .catch(err => console.error(err));
```

### 3. Print Receipt (`POST /print`)
Accepts JSON receipt data and issues formatted thermal ESC/POS commands:

```javascript
const receiptData = {
  printerName: "RPP02N", // Optional. If omitted, uses DEFAULT_PRINTER_NAME
  storeName: "KOPI ADDICT",
  cashier: "Adam",
  invoiceNo: "INV-20260528-01",
  items: [
    {
      name: "Signature Fried Rice Special Extra Hot",
      qty: 1,
      price: 28000,
      total: 28000
    },
    {
      name: "Iced Sweet Jasmine Tea",
      qty: 2,
      price: 6000,
      total: 12000
    },
    {
      name: "Crispy Potato Chips Pack",
      qty: 3,
      price: 3000,
      total: 9000
    }
  ],
  subtotal: 49000,
  discount: 4000,
  tax: 4500,
  grandTotal: 49500,
  paymentMethod: "QRIS",
  paid: 49500,
  change: 0,
  footer: "POWERED BY GO PRINT AGENT\nTHANK YOU FOR VISITING!",
  kickDrawer: true, // Trigger RJ11 cash drawer kick-out
  skipCut: false    // Feed paper and execute partial tear-cut automatically
};

fetch("http://localhost:7878/print", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-Print-Agent-Key": "your-secret-key-if-configured"
  },
  body: JSON.stringify(receiptData)
})
  .then(res => res.json())
  .then(result => console.log("Print result:", result))
  .catch(err => console.error("Failed to print:", err));
```

### 4. Trigger Test Print (`POST /test-print`)
Sends a default mock receipt to check printer configuration:

```javascript
fetch("http://localhost:7878/test-print", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-Print-Agent-Key": "your-secret-key-if-configured"
  },
  body: JSON.stringify({
    printerName: "RPP02N" // Optional override
  })
})
  .then(res => res.json())
  .then(data => console.log("Test print outcome:", data))
  .catch(err => console.error(err));
```
