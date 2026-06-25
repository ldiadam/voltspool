package config

import (
	"os"
)

// Config holds all service configurations loaded from environment variables.
type Config struct {
	BindAddr           string   // IP address to bind to (e.g. "127.0.0.1")
	Port               string   // Port to listen on (e.g. "7878")
	HttpsPort          string   // HTTPS port to listen on (e.g. "7879")
	DefaultPrinterName string   // Default printer name when none is supplied
	PrintAgentKey      string   // Optional security token via X-Print-Agent-Key header
}

// LoadConfig reads configuration from the environment and falls back to safe defaults.
func LoadConfig() *Config {
	bindAddr := getEnv("PRINT_AGENT_BIND", "127.0.0.1")
	port := getEnv("PRINT_AGENT_PORT", "7878")
	httpsPort := getEnv("PRINT_AGENT_HTTPS_PORT", "7879")
	defaultPrinter := getEnv("DEFAULT_PRINTER_NAME", "RPP02N")
	key := getEnv("PRINT_AGENT_KEY", "")

	return &Config{
		BindAddr:           bindAddr,
		Port:               port,
		HttpsPort:          httpsPort,
		DefaultPrinterName: defaultPrinter,
		PrintAgentKey:      key,
	}
}

// getEnv retrieves an environment variable or returns a fallback default.
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}
