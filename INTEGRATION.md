# VoltSpool — Integration Manual

This guide covers how to send print jobs to VoltSpool from any application (web, desktop, mobile, or CLI) via its HTTP REST API.

---

## Quick Start

1. Launch VoltSpool GUI and click **Activate Print Daemon**
2. The server starts on `http://127.0.0.1:7878` (HTTP) and `https://127.0.0.1:7879` (HTTPS)
3. Send a POST request to `/print` with your receipt JSON

```bash
curl -X POST http://127.0.0.1:7878/print \
  -H "Content-Type: application/json" \
  -d '{
    "storeName": "MY STORE",
    "cashier": "Adam",
    "invoiceNo": "INV-001",
    "items": [{"name": "Coffee", "qty": 1, "price": 15000, "total": 15000}],
    "subtotal": 15000,
    "grandTotal": 15000,
    "paymentMethod": "CASH",
    "paid": 20000,
    "change": 5000,
    "footer": "THANK YOU!"
  }'
```

---

## Base URLs

| Protocol | URL | Notes |
|---|---|---|
| HTTP | `http://127.0.0.1:7878` | Default, no cert needed |
| HTTPS | `https://127.0.0.1:7879` | Auto-generated self-signed cert |

Both serve the same API. Use HTTPS if your client enforces secure connections.

---

## Authentication

If a security key is configured in the GUI (Security Key field), all protected endpoints require:

```
X-Print-Agent-Key: <your-key-here>
```

Endpoints **without** auth: `GET /`, `GET /health`, `GET /ticket`, `GET /logo.png`

Endpoints **with** auth: `GET /printers`, `GET /printers/status`, `POST /print`, `POST /print-generic`, `POST /print-raw`, `POST /print-task`, `POST /test-print`, `POST /logo`

---

## Endpoints

### `GET /health`

Check if the daemon is running.

**Response:**
```json
{ "ok": true }
```

---

### `GET /printers`

List all printers installed on the host OS.

**Response:**
```json
[
  { "name": "RPP02N" },
  { "name": "XP-58" }
]
```

---

### `GET /printers/status?name=RPP02N`

Query the hardware status of a specific printer.

**Query params:** `name` or `printer` (falls back to default printer)

**Response:**
```json
{
  "printerName": "RPP02N",
  "status": "ONLINE"
}
```

Possible statuses: `ONLINE`, `OFFLINE`, `PAPER_OUT`, `PAPER_JAM`, `LID_OPEN`, `PAUSED`, `BUSY`, `NOT_FOUND`

---

### `POST /print` — Structured Receipt

Send a pre-structured receipt. VoltSpool compiles it to ESC/POS automatically.

**Request body:**
```json
{
  "printerName": "RPP02N",
  "storeName": "KOPI RAJIN",
  "cashier": "Adam",
  "invoiceNo": "INV-20260625-01",
  "items": [
    { "name": "Espresso", "qty": 1, "price": 18000, "total": 18000 },
    { "name": "Croissant", "qty": 2, "price": 12000, "total": 24000 }
  ],
  "subtotal": 42000,
  "discount": 2000,
  "tax": 4000,
  "grandTotal": 44000,
  "paymentMethod": "QRIS",
  "paid": 44000,
  "change": 0,
  "footer": "POWERED BY VOLTSPOOL\nTHANK YOU!",
  "kickDrawer": true,
  "skipCut": false,
  "useLogo": false,
  "width": 32
}
```

**Field reference:**

| Field | Type | Required | Description |
|---|---|---|---|
| `printerName` | string | No | Target printer. Falls back to default if omitted. |
| `storeName` | string | Yes | Displayed at top, double-width bold centered |
| `cashier` | string | Yes | Cashier/operator name |
| `invoiceNo` | string | Yes | Invoice or transaction number |
| `items` | array | Yes | Line items (see below) |
| `subtotal` | int | Yes | Sum before discount/tax (in Rupiah, no decimals) |
| `discount` | int | No | Discount amount |
| `tax` | int | No | Tax amount |
| `grandTotal` | int | Yes | Final amount to pay |
| `paymentMethod` | string | Yes | e.g. "CASH", "QRIS", "CARD" |
| `paid` | int | Yes | Amount paid by customer |
| `change` | int | Yes | Change to give back |
| `footer` | string | No | Centered footer text (supports `\n`) |
| `kickDrawer` | bool | No | Trigger RJ11 cash drawer kick |
| `skipCut` | bool | No | Skip auto paper cut |
| `useLogo` | bool | No | Prepend `logo.bin` if it exists |
| `width` | int | No | Characters per line: `32` (58mm) or `48` (80mm). Default: 32 |

**Item object:**

| Field | Type | Description |
|---|---|---|
| `name` | string | Item name (auto-wraps if > width) |
| `qty` | int | Quantity |
| `price` | int | Unit price |
| `total` | int | Line total (qty × price) |

**Response:**
```json
{
  "success": true,
  "message": "Job sent to printer RPP02N successfully"
}
```

---

### `POST /print-generic` — Custom Line-by-Line Layout

This is the **most flexible endpoint**. You define the receipt layout line by line using a simple DSL. This is how you create custom templates for any business type.

**Request body:**
```json
{
  "printerName": "RPP02N",
  "width": 32,
  "lines": [
    { "type": "logo" },
    { "type": "text", "text": "MY BUSINESS NAME", "align": "CENTER", "bold": true, "size": "DOUBLE_HW" },
    { "type": "text", "text": "Jl. Example No. 123", "align": "CENTER" },
    { "type": "feed", "lines": 1 },
    { "type": "divider", "char": "-" },
    { "type": "text", "text": "Date: 2026-06-25" },
    { "type": "text", "text": "Invoice: INV-001" },
    { "type": "divider", "char": "-" },
    { "type": "columns", "left": "Item A", "right": "Rp 15.000" },
    { "type": "columns", "left": "Item B", "right": "Rp 25.000" },
    { "type": "divider", "char": "=" },
    { "type": "columns", "left": "TOTAL", "right": "Rp 40.000", "bold": true },
    { "type": "feed", "lines": 2 },
    { "type": "text", "text": "Thank you!", "align": "CENTER" },
    { "type": "cut" }
  ]
}
```

#### Line Types

**`text`** — Print a text line

| Field | Values | Default | Description |
|---|---|---|---|
| `text` | string | — | The text content |
| `align` | `"LEFT"`, `"CENTER"`, `"RIGHT"` | `"LEFT"` | Text alignment |
| `size` | `"NORMAL"`, `"DOUBLE_HEIGHT"`, `"DOUBLE_WIDTH"`, `"DOUBLE_HW"` | `"NORMAL"` | Font size |
| `bold` | bool | `false` | Bold text |

Long text auto-wraps to fit the printer width.

**`columns`** — Two-column line (left-aligned + right-aligned)

| Field | Values | Default | Description |
|---|---|---|---|
| `left` | string | — | Left column text |
| `right` | string | — | Right column text |
| `size` | same as text | `"NORMAL"` | Font size |
| `bold` | bool | `false` | Bold text |

Spaces between columns are auto-calculated.

**`divider`** — Horizontal line

| Field | Values | Default | Description |
|---|---|---|---|
| `char` | string | `"-"` | Character to repeat across width |

Use `"="` for double-lines, `"~"` for decorative, etc.

**`feed`** — Feed paper

| Field | Values | Default | Description |
|---|---|---|---|
| `lines` | int | `1` | Number of lines to feed |

**`cut`** — Feed and partial cut

No fields. Feeds paper to the cutter and executes a partial cut.

**`drawer`** — Kick cash drawer

No fields. Sends the RJ11 drawer kick pulse.

**`logo`** — Print cached logo

No fields. Reads `logo.bin` from the working directory (uploaded via `POST /logo`). Prints it centered.

---

### `POST /print-raw` — Raw ESC/POS Bytes

Send pre-built ESC/POS byte commands as base64. For advanced users who build their own ESC/POS payloads.

**Request body:**
```json
{
  "printerName": "RPP02N",
  "base64Data": "G1tAQHRlc3QgZGF0YQo="
}
```

| Field | Type | Description |
|---|---|---|
| `printerName` | string | Target printer |
| `base64Data` | string | Base64-encoded raw ESC/POS byte payload |

**Generate base64 from JavaScript:**
```javascript
// Build ESC/POS commands
const encoder = new TextEncoder();
const initCmd = new Uint8Array([0x1b, 0x40]);        // ESC @ (initialize)
const cutCmd  = new Uint8Array([0x1d, 0x56, 0x42, 0x00]); // GS V B 0 (cut)
const text    = encoder.encode("Hello World\n\n\n");

// Combine and encode
const payload = new Uint8Array([...initCmd, ...text, ...cutCmd]);
const base64 = btoa(String.fromCharCode(...payload));

fetch("http://127.0.0.1:7878/print-raw", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ printerName: "RPP02N", base64Data: base64 })
});
```

---

### `POST /print-task` — Workforce Task Ticket

Print a structured task/dispatch ticket with checkoff fields.

**Request body:**
```json
{
  "printerName": "RPP02N",
  "taskId": "TSK-001",
  "title": "Restock Paper Rolls",
  "description": "Fetch 2 boxes from storage room B",
  "priority": "HIGH",
  "assignee": "Adam",
  "dueDate": "25-06-2026",
  "kickDrawer": false,
  "skipCut": false
}
```

| Field | Type | Description |
|---|---|---|
| `taskId` | string | Unique ticket identifier |
| `title` | string | Task title (bold, auto-wraps) |
| `description` | string | Detailed description |
| `priority` | string | `"LOW"`, `"MEDIUM"`, or `"HIGH"` |
| `assignee` | string | Staff member name |
| `dueDate` | string | Optional due date (shows current date if omitted) |

---

### `POST /test-print` — Test Receipt

Print a hardcoded test receipt to verify printer connectivity.

**Request body (optional):**
```json
{
  "printerName": "RPP02N"
}
```

---

### `POST /logo` — Upload Store Logo

Upload a PNG or JPEG image. VoltSpool resizes it to 384px width, converts to 1-bit monochrome, and caches as `logo.bin` for the `useLogo` / `logo` line type.

```bash
curl -X POST http://127.0.0.1:7878/logo \
  -F "file=@my-store-logo.png"
```

**Response:**
```json
{
  "success": true,
  "message": "Store logo successfully compiled and cached as logo.bin on server!"
}
```

---

## Integration Examples

### JavaScript / Web App

```javascript
const API = "http://127.0.0.1:7878";
const HEADERS = { "Content-Type": "application/json" };

// 1. Check health
const health = await fetch(`${API}/health`).then(r => r.json());
console.log("Daemon running:", health.ok);

// 2. List printers
const printers = await fetch(`${API}/printers`, { headers: HEADERS }).then(r => r.json());
console.log("Available:", printers);

// 3. Print a receipt
await fetch(`${API}/print`, {
  method: "POST",
  headers: HEADERS,
  body: JSON.stringify({
    storeName: "MY CAFE",
    cashier: "Rina",
    invoiceNo: "INV-" + Date.now(),
    items: [
      { name: "Latte", qty: 1, price: 22000, total: 22000 },
      { name: "Muffin", qty: 2, price: 15000, total: 30000 }
    ],
    subtotal: 52000,
    tax: 5200,
    grandTotal: 57200,
    paymentMethod: "CASH",
    paid: 60000,
    change: 2800,
    footer: "See you again!"
  })
});

// 4. Print a custom layout (e.g., parking ticket)
await fetch(`${API}/print-generic`, {
  method: "POST",
  headers: HEADERS,
  body: JSON.stringify({
    width: 32,
    lines: [
      { type: "text", text: "PARKING TICKET", align: "CENTER", bold: true, size: "DOUBLE_HW" },
      { type: "divider", char: "=" },
      { type: "columns", left: "Vehicle", "right": "B 1234 CD" },
      { type: "columns", left: "Entry", "right": "14:30" },
      { type: "columns", left: "Rate", "right": "Rp 5.000/jam" },
      { type: "divider", char: "-" },
      { type: "text", text: "Show this ticket on exit", "align": "CENTER" },
      { type: "cut" }
    ]
  })
});
```

### Python

```python
import requests

API = "http://127.0.0.1:7878"

# Print receipt
requests.post(f"{API}/print", json={
    "storeName": "WARUNG TEH",
    "cashier": "Budi",
    "invoiceNo": "WT-042",
    "items": [
        {"name": "Teh Manis", "qty": 3, "price": 5000, "total": 15000},
        {"name": " Gorengan", "qty": 5, "price": 2000, "total": 10000}
    ],
    "subtotal": 25000,
    "grandTotal": 25000,
    "paymentMethod": "CASH",
    "paid": 30000,
    "change": 5000,
    "footer": "TERIMA KASIH!"
})

# Custom layout
requests.post(f"{API}/print-generic", json={
    "width": 32,
    "lines": [
        {"type": "text", "text": "QUEUE NUMBER", "align": "CENTER", "bold": True, "size": "DOUBLE_HW"},
        {"type": "text", "text": "A-042", "align": "CENTER", "size": "DOUBLE_HW", "bold": True},
        {"type": "feed", "lines": 1},
        {"type": "text", "text": "Please wait for your turn", "align": "CENTER"},
        {"type": "cut"}
    ]
})
```

### cURL

```bash
# Health check
curl http://127.0.0.1:7878/health

# List printers
curl http://127.0.0.1:7878/printers

# Print receipt
curl -X POST http://127.0.0.1:7878/print \
  -H "Content-Type: application/json" \
  -d '{"storeName":"TEST","cashier":"Op","invoiceNo":"T-1","items":[{"name":"Item","qty":1,"price":10000,"total":10000}],"subtotal":10000,"grandTotal":10000,"paymentMethod":"CASH","paid":10000,"change":0}'

# Custom layout (parking ticket)
curl -X POST http://127.0.0.1:7878/print-generic \
  -H "Content-Type: application/json" \
  -d '{"width":32,"lines":[{"type":"text","text":"PARKING","align":"CENTER","bold":true,"size":"DOUBLE_HW"},{"type":"columns","left":"Car","right":"B 1234"},{"type":"cut"}]}'

# Upload logo
curl -X POST http://127.0.0.1:7878/logo -F "file=@logo.png"
```

---

## Template Recipes

### Restaurant Receipt
```json
{
  "width": 32,
  "lines": [
    { "type": "logo" },
    { "type": "text", "text": "RESTO MAKMUR", "align": "CENTER", "bold": true, "size": "DOUBLE_HW" },
    { "type": "text", "text": "Jl. Sudirman No. 10", "align": "CENTER" },
    { "type": "text", "text": "Telp: 021-5551234", "align": "CENTER" },
    { "type": "feed", "lines": 1 },
    { "type": "divider", "char": "-" },
    { "type": "columns", "left": "No", "right": "INV-20260625-001" },
    { "type": "columns", "left": "Table", "right": "12" },
    { "type": "columns", "left": "Date", "right": "25/06/2026 19:30" },
    { "type": "divider", "char": "-" },
    { "type": "columns", "left": "1x Nasi Goreng", "right": "Rp 28.000" },
    { "type": "columns", "left": "2x Es Teh", "right": "Rp 12.000" },
    { "type": "columns", "left": "1x Ayam Bakar", "right": "Rp 45.000" },
    { "type": "divider", "char": "-" },
    { "type": "columns", "left": "Subtotal", "right": "Rp 85.000" },
    { "type": "columns", "left": "PPN 10%", "right": "Rp 8.500" },
    { "type": "divider", "char": "=" },
    { "type": "columns", "left": "TOTAL", "right": "Rp 93.500", "bold": true },
    { "type": "divider", "char": "-" },
    { "type": "columns", "left": "Payment", "right": "QRIS" },
    { "type": "columns", "left": "Paid", "right": "Rp 93.500" },
    { "type": "feed", "lines": 1 },
    { "type": "text", "text": "Selamat menikmati!", "align": "CENTER" },
    { "type": "cut" }
  ]
}
```

### Queue Number
```json
{
  "width": 32,
  "lines": [
    { "type": "text", "text": "ANTRIAN", "align": "CENTER", "bold": true, "size": "DOUBLE_HW" },
    { "type": "feed", "lines": 1 },
    { "type": "text", "text": "B-017", "align": "CENTER", "size": "DOUBLE_HW", "bold": true },
    { "type": "feed", "lines": 1 },
    { "type": "divider", "char": "-" },
    { "type": "text", "text": "Silakan tunggu", "align": "CENTER" },
    { "type": "text", "text": "panggilan Anda", "align": "CENTER" },
    { "type": "divider", "char": "-" },
    { "type": "cut" }
  ]
}
```

### Delivery Order
```json
{
  "width": 32,
  "lines": [
    { "type": "text", "text": "DELIVERY ORDER", "align": "CENTER", "bold": true, "size": "DOUBLE_HW" },
    { "type": "divider", "char": "=" },
    { "type": "columns", "left": "Order", "right": "#DL-8812" },
    { "type": "columns", "left": "Date", "right": "25/06/2026" },
    { "type": "columns", "left": "Driver", "right": "Andi" },
    { "type": "divider", "char": "-" },
    { "type": "text", "text": "Customer:", "bold": true },
    { "type": "text", "text": "Budi Santoso" },
    { "type": "text", "text": "Jl. Melati No. 5, Rt 03/Rw 02" },
    { "type": "text", "text": "Telp: 081234567890" },
    { "type": "divider", "char": "-" },
    { "type": "text", "text": "Items:", "bold": true },
    { "type": "columns", "left": "2x Nasi Padang", "right": "Rp 56.000" },
    { "type": "columns", "left": "1x Es Jeruk", "right": "Rp 8.000" },
    { "type": "divider", "char": "=" },
    { "type": "columns", "left": "TOTAL", "right": "Rp 64.000", "bold": true },
    { "type": "columns", "left": "Payment", "right": "COD" },
    { "type": "feed", "lines": 1 },
    { "type": "text", "text": "CHECKED IN  : _____:_____", "align": "LEFT" },
    { "type": "text", "text": "DELIVERED   : _____:_____", "align": "LEFT" },
    { "type": "text", "text": "SIGNATURE   : ________________", "align": "LEFT" },
    { "type": "cut" }
  ]
}
```

---

## ESC/POS Reference

If you use `/print-raw`, here are the common ESC/POS commands:

| Command | Bytes | Description |
|---|---|---|
| Initialize | `1B 40` | Reset printer to defaults |
| Align Left | `1B 61 00` | Set text alignment |
| Align Center | `1B 61 01` | |
| Align Right | `1B 61 02` | |
| Bold On | `1B 45 01` | |
| Bold Off | `1B 45 00` | |
| Normal Size | `1D 21 00` | 1× width, 1× height |
| Double Height | `1D 21 01` | 1× width, 2× height |
| Double Width | `1D 21 10` | 2× width, 1× height |
| Double Both | `1D 21 11` | 2× width, 2× height |
| Feed n lines | `1B 64 n` | Feed paper by n lines |
| Cut | `1D 56 42 00` | Feed and partial cut |
| Cash Drawer | `1B 70 00 19 FC` | Kick RJ11 drawer pin 2 |

---

## Fake Printer (PDF Mode)

No thermal printer connected? VoltSpool has a built-in **PDF output mode** that generates thermal-paper-styled PDFs for testing.

### How to Use

1. In the **Receipt Simulator** tab, check the **"Output to PDF instead of printer"** checkbox
2. Configure your receipt template (store name, items, etc.)
3. Click **Compile & Print**
4. A PDF file is saved to the working directory (e.g. `receipt_20260625_143022.pdf`)
5. It auto-opens in your default PDF viewer

### What the PDF Looks Like

- **58mm paper width** (32mm printable) — matches real thermal paper proportions
- **Monospaced Courier font** — authentic receipt appearance
- **White background** with black text
- **Dashed tear line** at the bottom — simulates paper cut edge
- **Auto-sized height** — trims to fit content, no wasted blank space

### Programmatic PDF Generation

You can also generate PDFs from the API:

```bash
curl -X POST http://127.0.0.1:7878/print-generic \
  -H "Content-Type: application/json" \
  -d '{"width":32,"lines":[...]}'
```

Then use the `/print` endpoint with `skipCut: true` to get clean text output.

For direct PDF generation from your app, use the Go package:

```go
import "print-agent/internal/pdfprinter"

err := pdfprinter.GenerateReceiptPDF(receiptText, "output.pdf")
```

| Printer Type | Width (chars) | `width` value |
|---|---|---|
| 58mm thermal (RPP02N, XP-58) | 32 characters | `32` |
| 80mm thermal (XP-80, TM-T82) | 48 characters | `48` |

All text wrapping and divider lines respect the `width` setting.
