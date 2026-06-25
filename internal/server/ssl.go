package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// EnsureCertificatesExist checks if local SSL cert.pem and key.pem exist.
// If they do not, it generates a 10-year custom Root CA and signs a 10-year server certificate.
func EnsureCertificatesExist() (string, string, error) {
	caCertFile := "ca.pem"
	caKeyFile := "ca.key"
	certFile := "cert.pem"
	keyFile := "key.pem"

	// Check if the server cert and key already exist
	_, errCert := os.Stat(certFile)
	_, errKey := os.Stat(keyFile)
	if errCert == nil && errKey == nil {
		return certFile, keyFile, nil
	}

	log.Println("[SSL] Local SSL/TLS certificates not found. Starting custom 10-Year Root CA generation process...")

	// 1. Generate or load the Root CA
	var caTemplate *x509.Certificate
	var caKey *rsa.PrivateKey

	_, errCACert := os.Stat(caCertFile)
	_, errCAKey := os.Stat(caKeyFile)

	if errCACert != nil || errCAKey != nil {
		log.Println("[SSL] Generating new 10-Year Root CA (ca.pem & ca.key)...")
		
		var err error
		caKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate CA private key: %w", err)
		}

		serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
		serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate CA serial number: %w", err)
		}

		caTemplate = &x509.Certificate{
			SerialNumber: serialNumber,
			Subject: pkix.Name{
				Organization: []string{"Go Print Agent CA"},
				CommonName:   "Go Local Print Agent Root CA",
			},
			NotBefore:             time.Now().Add(-1 * time.Hour),
			NotAfter:              time.Now().AddDate(10, 0, 0), // 10 years validity
			IsCA:                  true,
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
			BasicConstraintsValid: true,
		}

		caBytes, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
		if err != nil {
			return "", "", fmt.Errorf("failed to create CA certificate: %w", err)
		}

		// Write ca.pem
		caCertOut, err := os.Create(caCertFile)
		if err != nil {
			return "", "", fmt.Errorf("failed to open ca.pem for writing: %w", err)
		}
		pem.Encode(caCertOut, &pem.Block{Type: "CERTIFICATE", Bytes: caBytes})
		caCertOut.Close()

		// Write ca.key
		caKeyOut, err := os.OpenFile(caKeyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return "", "", fmt.Errorf("failed to open ca.key for writing: %w", err)
		}
		caPrivBytes, err := x509.MarshalPKCS8PrivateKey(caKey)
		if err != nil {
			caKeyOut.Close()
			return "", "", fmt.Errorf("failed to marshal CA private key: %w", err)
		}
		pem.Encode(caKeyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: caPrivBytes})
		caKeyOut.Close()

		log.Println("[SSL] Root CA certificate successfully generated!")

		// Attempt to auto-register CA certificate for seamless trust
		autoRegisterCert(caCertFile)

		// Print fallback instructions just in case
		printTrustInstructions(caCertFile)
	} else {
		log.Println("[SSL] Existing Root CA found. Loading...")
		// Load existing ca.pem
		caCertBytes, err := os.ReadFile(caCertFile)
		if err != nil {
			return "", "", fmt.Errorf("failed to read ca.pem: %w", err)
		}
		block, _ := pem.Decode(caCertBytes)
		if block == nil {
			return "", "", fmt.Errorf("failed to decode ca.pem block")
		}
		caTemplate, err = x509.ParseCertificate(block.Bytes)
		if err != nil {
			return "", "", fmt.Errorf("failed to parse ca.pem: %w", err)
		}

		// Load existing ca.key
		caKeyBytes, err := os.ReadFile(caKeyFile)
		if err != nil {
			return "", "", fmt.Errorf("failed to read ca.key: %w", err)
		}
		keyBlock, _ := pem.Decode(caKeyBytes)
		if keyBlock == nil {
			return "", "", fmt.Errorf("failed to decode ca.key block")
		}
		parsedKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if err != nil {
			return "", "", fmt.Errorf("failed to parse ca.key: %w", err)
		}
		var ok bool
		caKey, ok = parsedKey.(*rsa.PrivateKey)
		if !ok {
			return "", "", fmt.Errorf("loaded CA key is not an RSA private key")
		}
	}

	// 2. Generate and Sign Server Certificate using our Root CA
	log.Println("[SSL] Generating and signing new 10-Year Local Server Certificate...")
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate server private key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate server serial number: %w", err)
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Go Print Agent Server"},
			CommonName:   "127.0.0.1",
		},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0), // 10 years validity
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}

	serverBytes, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to create server certificate: %w", err)
	}

	// Write cert.pem
	certOut, err := os.Create(certFile)
	if err != nil {
		return "", "", fmt.Errorf("failed to open cert.pem for writing: %w", err)
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: serverBytes})
	certOut.Close()

	// Write key.pem
	keyOut, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return "", "", fmt.Errorf("failed to open key.pem for writing: %w", err)
	}
	serverPrivBytes, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		keyOut.Close()
		return "", "", fmt.Errorf("failed to marshal server private key: %w", err)
	}
	pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: serverPrivBytes})
	keyOut.Close()

	log.Println("[SSL] Local Server Certificate successfully generated and signed by custom Root CA!")
	return certFile, keyFile, nil
}

// autoRegisterCert attempts to trust the generated Root CA automatically.
func autoRegisterCert(caFile string) {
	absPath, err := filepath.Abs(caFile)
	if err != nil {
		absPath = caFile
	}

	switch runtime.GOOS {
	case "windows":
		log.Println("[SSL] Windows: Attempting to silently register Root CA in Current User's Trust Store...")
		cmd := exec.Command("certutil", "-addstore", "-user", "-f", "Root", absPath)
		if err := cmd.Run(); err != nil {
			log.Printf("[SSL] Windows: Auto-registration failed (might need manual admin Elevation): %v", err)
		} else {
			log.Println("[SSL] Windows: Root CA successfully registered in Current User's Trust Store! Chrome/Edge will now trust localhost SSL natively.")
		}
	case "darwin":
		log.Println("[SSL] macOS: Attempting to register Root CA in User's Login Keychain (TouchID/Password prompt may appear)...")
		// Find login keychain
		home, err := os.UserHomeDir()
		if err != nil {
			log.Printf("[SSL] macOS: Could not resolve home directory: %v", err)
			return
		}
		loginKeychain := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
		// Check if login.keychain-db exists, fallback to older login.keychain name
		if _, err := os.Stat(loginKeychain); err != nil {
			loginKeychain = filepath.Join(home, "Library", "Keychains", "login.keychain")
		}

		cmd := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", loginKeychain, absPath)
		if err := cmd.Run(); err != nil {
			log.Printf("[SSL] macOS: Auto-registration failed or was cancelled by user: %v", err)
		} else {
			log.Println("[SSL] macOS: Root CA successfully trusted in User's Login Keychain!")
		}
	}
}

func printTrustInstructions(caFile string) {
	absPath, err := filepath.Abs(caFile)
	if err != nil {
		absPath = caFile
	}

	log.Println("")
	log.Println("================================================================================")
	log.Println("🔑 MANUAL TRUST BACKFALL GUIDE:")
	log.Println("If the automatic registration was skipped or requires manual verification:")
	log.Println("")

	switch runtime.GOOS {
	case "darwin":
		log.Printf("👉 macOS Command:\n   sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %q\n", absPath)
	case "windows":
		log.Printf("👉 Windows (Run as Admin):\n   certutil -addstore -f \"Root\" %q\n", absPath)
	default:
		log.Printf("👉 Linux Command:\n   sudo cp %q /usr/local/share/ca-certificates/ && sudo update-ca-certificates\n", absPath)
	}
	log.Println("================================================================================")
	log.Println("")
}
