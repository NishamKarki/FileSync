// Author: Rabindra Neupane
package network

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	bootstrapServerName     = "filesync-bootstrap.internal"
	deviceURIPrefix         = "urn:filesync:device:"
	coordinatorCertLifetime = 5 * 365 * 24 * time.Hour
	peerCertLifetime        = 24 * time.Hour
	peerCertRenewBefore     = 12 * time.Hour
	caCertFileName          = "ca-cert.pem"
	caKeyFileName           = "ca-key.pem"
	peerCertFileName        = "peer-cert.pem"
	peerKeyFileName         = "peer-key.pem"
	deviceSettingsFileName  = ".filesync-device.json"
)

// defaultSecurityConfigDir returns the per-user OS configuration directory for
// FileSync credentials, keys, and certificates with 0700 permissions.
func defaultSecurityConfigDir() (string, error) {
	baseDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	dir := filepath.Join(baseDir, "FileSync")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create FileSync config directory: %w", err)
	}
	_ = os.Chmod(dir, 0700)
	return dir, nil
}

func writeProtectedFile(path string, data []byte, encrypt bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0700)

	payload := data
	if encrypt {
		var err error
		payload, err = protectLocalSecret(data)
		if err != nil {
			return err
		}
	}

	tempFile, err := os.CreateTemp(dir, ".filesync-sec-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tempFile.Name())

	if err := tempFile.Chmod(0600); err != nil {
		tempFile.Close()
		return err
	}
	if _, err := tempFile.Write(payload); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempFile.Name(), path); err != nil {
		return err
	}
	_ = os.Chmod(path, 0600)
	return nil
}

func readProtectedFile(path string, decrypt bool) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !decrypt {
		return raw, nil
	}
	return unprotectLocalSecret(raw)
}

func randomCertSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, err
	}
	if serial.Sign() <= 0 {
		serial.SetInt64(1)
	}
	return serial, nil
}

func certFingerprintSHA256(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func encodeECPrivateKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func decodeECPrivateKeyPEM(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("invalid EC private key PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	if key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("expected ECDSA P-256 private key")
	}
	return key, nil
}

func encodeCertPEM(certDER []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
}

func decodeCertPEM(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("invalid certificate PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

// ensureCoordinatorCA loads an existing Coordinator Root CA from configDir or
// creates and persists a fresh ECDSA P-256 Root CA (`ca-cert.pem`, `ca-key.pem`).
func ensureCoordinatorCA(configDir string) (*x509.Certificate, *ecdsa.PrivateKey, string, string, error) {
	caCertPath := filepath.Join(configDir, caCertFileName)
	caKeyPath := filepath.Join(configDir, caKeyFileName)

	certPEM, certErr := readProtectedFile(caCertPath, false)
	keyPEM, keyErr := readProtectedFile(caKeyPath, true)
	if certErr == nil && keyErr == nil {
		caCert, parseCertErr := decodeCertPEM(certPEM)
		caKey, parseKeyErr := decodeECPrivateKeyPEM(keyPEM)
		if parseCertErr == nil && parseKeyErr == nil && caCert.IsCA &&
			time.Now().Add(30*24*time.Hour).Before(caCert.NotAfter) {
			return caCert, caKey, string(certPEM), certFingerprintSHA256(caCert), nil
		}
	}

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("generate coordinator CA key: %w", err)
	}
	serial, err := randomCertSerial()
	if err != nil {
		return nil, nil, "", "", err
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "FileSync Coordinator Root CA",
			Organization: []string{"FileSync"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(coordinatorCertLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("create coordinator CA certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, "", "", err
	}
	newKeyPEM, err := encodeECPrivateKeyPEM(caKey)
	if err != nil {
		return nil, nil, "", "", err
	}
	newCertPEM := encodeCertPEM(certDER)
	if err := writeProtectedFile(caKeyPath, newKeyPEM, true); err != nil {
		return nil, nil, "", "", err
	}
	if err := writeProtectedFile(caCertPath, newCertPEM, false); err != nil {
		return nil, nil, "", "", err
	}
	return caCert, caKey, string(newCertPEM), certFingerprintSHA256(caCert), nil
}

// ensurePeerKeyPair loads or creates this device's ECDSA P-256 keypair in configDir.
func ensurePeerKeyPair(configDir string) (*ecdsa.PrivateKey, error) {
	keyPath := filepath.Join(configDir, peerKeyFileName)
	if rawKey, err := readProtectedFile(keyPath, true); err == nil {
		if key, parseErr := decodeECPrivateKeyPEM(rawKey); parseErr == nil {
			return key, nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate peer private key: %w", err)
	}
	keyPEM, err := encodeECPrivateKeyPEM(key)
	if err != nil {
		return nil, err
	}
	if err := writeProtectedFile(keyPath, keyPEM, true); err != nil {
		return nil, err
	}
	return key, nil
}

func appendUniqueIP(ips []net.IP, candidate net.IP) []net.IP {
	if candidate == nil {
		return ips
	}
	for _, existing := range ips {
		if existing.Equal(candidate) {
			return ips
		}
	}
	return append(ips, candidate)
}

func createPeerCSR(key *ecdsa.PrivateKey, deviceID, deviceName, localIP string) ([]byte, error) {
	commonName := deviceID
	if commonName == "" {
		commonName = "pending-device"
	}
	var ips []net.IP
	if parsed := net.ParseIP(strings.TrimSpace(localIP)); parsed != nil {
		ips = appendUniqueIP(ips, parsed)
	}
	ips = appendUniqueIP(ips, net.ParseIP("127.0.0.1"))
	ips = appendUniqueIP(ips, net.ParseIP("::1"))

	dnsNames := []string{"localhost"}
	if trimmed := strings.TrimSpace(deviceName); trimmed != "" && !strings.ContainsAny(trimmed, " \t\r\n/\\") {
		dnsNames = append(dnsNames, trimmed)
	}

	var uris []*url.URL
	if deviceID != "" {
		if parsedURI, err := url.Parse(deviceURIPrefix + deviceID); err == nil {
			uris = []*url.URL{parsedURI}
		}
	}

	template := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"FileSync Peer"},
		},
		DNSNames:    dnsNames,
		IPAddresses: ips,
		URIs:        uris,
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &template, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}), nil
}

func signPeerCSR(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, csrPEM []byte, assignedDeviceID, peerIP, peerName string) (string, *x509.Certificate, error) {
	if caCert == nil || caKey == nil {
		return "", nil, fmt.Errorf("coordinator CA is unavailable")
	}
	if !isHexToken(assignedDeviceID, 16) {
		return "", nil, fmt.Errorf("invalid assigned device ID for peer certificate")
	}
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return "", nil, fmt.Errorf("invalid certificate signing request")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return "", nil, fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return "", nil, fmt.Errorf("invalid CSR signature: %w", err)
	}
	pubKey, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || pubKey.Curve != elliptic.P256() {
		return "", nil, fmt.Errorf("CSR must use an ECDSA P-256 key")
	}

	serial, err := randomCertSerial()
	if err != nil {
		return "", nil, err
	}
	var ips []net.IP
	if parsed := net.ParseIP(strings.TrimSpace(peerIP)); parsed != nil {
		ips = appendUniqueIP(ips, parsed)
	}
	ips = appendUniqueIP(ips, net.ParseIP("127.0.0.1"))
	ips = appendUniqueIP(ips, net.ParseIP("::1"))

	dnsNames := []string{"localhost"}
	if trimmed := strings.TrimSpace(peerName); trimmed != "" && !strings.ContainsAny(trimmed, " \t\r\n/\\") {
		dnsNames = append(dnsNames, trimmed)
	}
	deviceURI, err := url.Parse(deviceURIPrefix + assignedDeviceID)
	if err != nil {
		return "", nil, err
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   assignedDeviceID,
			Organization: []string{"FileSync Peer"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(peerCertLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
		URIs:                  []*url.URL{deviceURI},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, caCert, pubKey, caKey)
	if err != nil {
		return "", nil, fmt.Errorf("sign peer certificate: %w", err)
	}
	leafCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return "", nil, err
	}
	return string(encodeCertPEM(certDER)), leafCert, nil
}

func isHexToken(value string, expectedBytes int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == expectedBytes && value == strings.ToLower(value)
}

func deviceIDFromPeerCert(cert *x509.Certificate) (string, bool) {
	if cert == nil {
		return "", false
	}
	cn := cert.Subject.CommonName
	if !isHexToken(cn, 16) {
		return "", false
	}
	expectedURI := deviceURIPrefix + cn
	for _, uri := range cert.URIs {
		if uri != nil && uri.String() == expectedURI {
			return cn, true
		}
	}
	return "", false
}

// VerifiedPeerDeviceID extracts the authenticated DeviceID from an mTLS request's verified certificate chain.
func VerifiedPeerDeviceID(request *http.Request) (string, bool) {
	if request == nil || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 || len(request.TLS.VerifiedChains[0]) == 0 {
		return "", false
	}
	return deviceIDFromPeerCert(request.TLS.VerifiedChains[0][0])
}

func generateBootstrapTLSCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := randomCertSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	if localIP, ipErr := GetLocalIP(); ipErr == nil {
		ips = appendUniqueIP(ips, net.ParseIP(localIP))
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "FileSync Unpaired Bootstrap"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(7 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           ips,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(certDER)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
		Leaf:        leaf,
	}, nil
}

func buildPeerTLSCert(certPEM string, caCert *x509.Certificate, key *ecdsa.PrivateKey) (tls.Certificate, *x509.Certificate, error) {
	leaf, err := decodeCertPEM([]byte(certPEM))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	if key == nil {
		return tls.Certificate{}, nil, fmt.Errorf("peer private key is unavailable")
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return tls.Certificate{}, nil, fmt.Errorf("peer certificate does not match local private key")
	}
	chain := [][]byte{leaf.Raw}
	if caCert != nil {
		chain = append(chain, caCert.Raw)
	}
	return tls.Certificate{
		Certificate: chain,
		PrivateKey:  key,
		Leaf:        leaf,
	}, leaf, nil
}

func verifyLeafMatchesCA(leaf, ca *x509.Certificate, now time.Time) error {
	if leaf == nil || ca == nil {
		return errors.New("missing certificate or CA")
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:       pool,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	return err
}

func parseCertPoolFromCert(cert *x509.Certificate) *x509.CertPool {
	if cert == nil {
		return nil
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return pool
}

// ServerTLSConfig returns a dynamic TLS configuration defaulting to mTLS
// (tls.RequireAndVerifyClientCert against the Coordinator Root CA), while
// permitting the bootstrap ALPN handshake used solely by /ping and /devices/register.
func (registry *DeviceRegistry) ServerTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ClientAuth: tls.RequireAndVerifyClientCert,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			return registry.tlsConfigForClient(hello), nil
		},
	}
}

func (registry *DeviceRegistry) tlsConfigForClient(hello *tls.ClientHelloInfo) *tls.Config {
	wantsBootstrap := hello != nil && hello.ServerName == bootstrapServerName

	if registry == nil {
		bootCert, err := generateBootstrapTLSCert()
		if err != nil {
			return &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert}
		}
		if wantsBootstrap {
			return &tls.Config{
				MinVersion:   tls.VersionTLS12,
				Certificates: []tls.Certificate{bootCert},
				ClientAuth:   tls.NoClientCert,
			}
		}
		return &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{bootCert},
			ClientAuth:   tls.RequireAndVerifyClientCert,
		}
	}

	registry.tlsMutex.RLock()
	caPool := registry.caPool
	cert := registry.peerTLSCert
	hasPeerCert := len(cert.Certificate) > 0
	bootCert := registry.bootstrapCert
	registry.tlsMutex.RUnlock()

	if !hasPeerCert {
		cert = bootCert
	}
	if wantsBootstrap {
		return &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
			ClientAuth:   tls.NoClientCert,
		}
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}
}

func (registry *DeviceRegistry) initTLSState() error {
	peerKey, err := ensurePeerKeyPair(registry.configDir)
	if err != nil {
		return err
	}
	bootCert, err := generateBootstrapTLSCert()
	if err != nil {
		return err
	}

	registry.tlsMutex.Lock()
	registry.peerKey = peerKey
	registry.bootstrapCert = bootCert
	registry.tlsMutex.Unlock()

	// Load persisted Coordinator CA if available.
	if caBytes, err := readProtectedFile(filepath.Join(registry.configDir, caCertFileName), false); err == nil {
		if caCert, parseErr := decodeCertPEM(caBytes); parseErr == nil && caCert.IsCA {
			caFP := certFingerprintSHA256(caCert)
			if registry.settings.CAFingerprint == "" || caFP == registry.settings.CAFingerprint {
				registry.tlsMutex.Lock()
				registry.caCert = caCert
				registry.caCertPEM = string(caBytes)
				registry.caFingerprint = caFP
				registry.caPool = parseCertPoolFromCert(caCert)
				registry.tlsMutex.Unlock()
			}
		}
	}

	if registry.settings.Coordinator {
		if err := registry.ensureCoordinatorPKILocked(); err != nil {
			return err
		}
	} else {
		// Load persisted peer certificate when it matches our key and CA.
		registry.tlsMutex.RLock()
		caCert := registry.caCert
		registry.tlsMutex.RUnlock()
		if caCert != nil {
			if peerBytes, err := readProtectedFile(filepath.Join(registry.configDir, peerCertFileName), false); err == nil {
				if tlsCert, leaf, buildErr := buildPeerTLSCert(string(peerBytes), caCert, peerKey); buildErr == nil {
					if verifyLeafMatchesCA(leaf, caCert, time.Now()) == nil {
						if certDevID, ok := deviceIDFromPeerCert(leaf); ok && (registry.settings.DeviceID == "" || certDevID == registry.settings.DeviceID) {
							registry.tlsMutex.Lock()
							registry.peerCert = leaf
							registry.peerTLSCert = tlsCert
							registry.tlsMutex.Unlock()
						}
					}
				}
			}
		}
	}

	registry.rebuildHTTPClients()
	return nil
}

func (registry *DeviceRegistry) ensureCoordinatorPKILocked() error {
	caCert, caKey, caPEM, caFP, err := ensureCoordinatorCA(registry.configDir)
	if err != nil {
		return err
	}
	registry.tlsMutex.Lock()
	registry.caCert = caCert
	registry.caKey = caKey
	registry.caCertPEM = caPEM
	registry.caFingerprint = caFP
	registry.caPool = parseCertPoolFromCert(caCert)
	peerKey := registry.peerKey
	existingPeerCert := registry.peerCert
	registry.tlsMutex.Unlock()

	deviceID := registry.settings.DeviceID
	localIP, _ := GetLocalIP()
	if deviceID != "" && peerKey != nil {
		needsIssue := existingPeerCert == nil || time.Now().Add(peerCertRenewBefore).After(existingPeerCert.NotAfter)
		if !needsIssue {
			if certID, ok := deviceIDFromPeerCert(existingPeerCert); !ok || certID != deviceID {
				needsIssue = true
			}
		}
		if needsIssue {
			csrPEM, csrErr := createPeerCSR(peerKey, deviceID, registry.name, localIP)
			if csrErr != nil {
				return csrErr
			}
			peerPEM, leaf, signErr := signPeerCSR(caCert, caKey, csrPEM, deviceID, localIP, registry.name)
			if signErr != nil {
				return signErr
			}
			if writeErr := writeProtectedFile(filepath.Join(registry.configDir, peerCertFileName), []byte(peerPEM), false); writeErr != nil {
				return writeErr
			}
			tlsCert, _, buildErr := buildPeerTLSCert(peerPEM, caCert, peerKey)
			if buildErr != nil {
				return buildErr
			}
			registry.tlsMutex.Lock()
			registry.peerCert = leaf
			registry.peerTLSCert = tlsCert
			registry.tlsMutex.Unlock()
		}
	}
	registry.rebuildHTTPClients()
	return nil
}

func (registry *DeviceRegistry) clearPeerTLSMaterial() {
	_ = os.Remove(filepath.Join(registry.configDir, caCertFileName))
	_ = os.Remove(filepath.Join(registry.configDir, peerCertFileName))
	registry.tlsMutex.Lock()
	registry.caCert = nil
	registry.caKey = nil
	registry.caCertPEM = ""
	registry.caFingerprint = ""
	registry.caPool = nil
	registry.peerCert = nil
	registry.peerTLSCert = tls.Certificate{}
	registry.tlsMutex.Unlock()
	registry.rebuildHTTPClients()
}

func (registry *DeviceRegistry) applyClientCertificates(caCertPEM, expectedFingerprint, peerCertPEM, assignedDeviceID string) error {
	caCert, err := decodeCertPEM([]byte(caCertPEM))
	if err != nil || !caCert.IsCA {
		return fmt.Errorf("invalid coordinator CA certificate")
	}
	actualFP := certFingerprintSHA256(caCert)
	if expectedFingerprint != "" && actualFP != expectedFingerprint {
		return fmt.Errorf("coordinator CA fingerprint mismatch")
	}

	registry.tlsMutex.RLock()
	peerKey := registry.peerKey
	registry.tlsMutex.RUnlock()
	if peerKey == nil {
		return fmt.Errorf("local peer private key unavailable")
	}
	tlsCert, leaf, err := buildPeerTLSCert(peerCertPEM, caCert, peerKey)
	if err != nil {
		return err
	}
	if err := verifyLeafMatchesCA(leaf, caCert, time.Now()); err != nil {
		return fmt.Errorf("peer certificate does not verify against coordinator CA: %w", err)
	}
	certDeviceID, ok := deviceIDFromPeerCert(leaf)
	if !ok || certDeviceID != assignedDeviceID {
		return fmt.Errorf("peer certificate device identity mismatch")
	}

	if err := writeProtectedFile(filepath.Join(registry.configDir, caCertFileName), []byte(caCertPEM), false); err != nil {
		return err
	}
	if err := writeProtectedFile(filepath.Join(registry.configDir, peerCertFileName), []byte(peerCertPEM), false); err != nil {
		return err
	}

	registry.tlsMutex.Lock()
	registry.caCert = caCert
	registry.caCertPEM = caCertPEM
	registry.caFingerprint = actualFP
	registry.caPool = parseCertPoolFromCert(caCert)
	registry.peerCert = leaf
	registry.peerTLSCert = tlsCert
	registry.tlsMutex.Unlock()

	registry.rebuildHTTPClients()
	return nil
}

func (registry *DeviceRegistry) needsPeerCertRenewal() bool {
	if registry == nil {
		return false
	}
	registry.tlsMutex.RLock()
	leaf := registry.peerCert
	ca := registry.caCert
	registry.tlsMutex.RUnlock()
	if leaf == nil || ca == nil {
		return true
	}
	return time.Now().Add(peerCertRenewBefore).After(leaf.NotAfter)
}

func (registry *DeviceRegistry) hasValidMTLSClientCert() bool {
	if registry == nil {
		return false
	}
	registry.tlsMutex.RLock()
	leaf := registry.peerCert
	ca := registry.caCert
	hasTLS := len(registry.peerTLSCert.Certificate) > 0
	registry.tlsMutex.RUnlock()
	if !hasTLS || leaf == nil || ca == nil {
		return false
	}
	return verifyLeafMatchesCA(leaf, ca, time.Now()) == nil
}

func (registry *DeviceRegistry) buildCSR(deviceID, localIP string) (string, error) {
	registry.tlsMutex.RLock()
	peerKey := registry.peerKey
	registry.tlsMutex.RUnlock()
	if peerKey == nil {
		return "", fmt.Errorf("peer private key is unavailable")
	}
	csrPEM, err := createPeerCSR(peerKey, deviceID, registry.name, localIP)
	if err != nil {
		return "", err
	}
	return string(csrPEM), nil
}

func (registry *DeviceRegistry) ClientTLSConfig() *tls.Config {
	if registry == nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	registry.tlsMutex.RLock()
	caPool := registry.caPool
	cert := registry.peerTLSCert
	registry.tlsMutex.RUnlock()

	var certs []tls.Certificate
	if len(cert.Certificate) > 0 {
		certs = []tls.Certificate{cert}
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      caPool,
		Certificates: certs,
	}
}

func (registry *DeviceRegistry) newMTLSHTTPClient(timeout time.Duration, maxIdlePerHost int) *http.Client {
	transport := &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: maxIdlePerHost,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     registry.ClientTLSConfig(),
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (registry *DeviceRegistry) rebuildHTTPClients() {
	if registry == nil {
		return
	}
	transfer := registry.newMTLSHTTPClient(2*time.Minute, 16)
	coord := registry.newMTLSHTTPClient(5*time.Second, 8)
	chunk := registry.newMTLSHTTPClient(10*time.Second, 8)

	registry.tlsMutex.Lock()
	oldTransfer := registry.transferClient
	oldCoord := registry.coordClient
	oldChunk := registry.chunkClient
	registry.transferClient = transfer
	registry.coordClient = coord
	registry.chunkClient = chunk
	registry.tlsMutex.Unlock()

	if oldTransfer != nil {
		oldTransfer.CloseIdleConnections()
	}
	if oldCoord != nil {
		oldCoord.CloseIdleConnections()
	}
	if oldChunk != nil {
		oldChunk.CloseIdleConnections()
	}
}

// TransferHTTPClient returns an mTLS-configured HTTP client for file and chunk transfers.
func (registry *DeviceRegistry) TransferHTTPClient() *http.Client {
	if registry == nil {
		return transferHTTPClient
	}
	registry.tlsMutex.RLock()
	client := registry.transferClient
	registry.tlsMutex.RUnlock()
	if client == nil {
		return transferHTTPClient
	}
	return client
}

// ChunkRetrievalHTTPClient returns an mTLS-configured HTTP client for peer chunk retrieval.
func (registry *DeviceRegistry) ChunkRetrievalHTTPClient() *http.Client {
	if registry == nil {
		return chunkRetrievalHTTPClient
	}
	registry.tlsMutex.RLock()
	client := registry.chunkClient
	registry.tlsMutex.RUnlock()
	if client == nil {
		return chunkRetrievalHTTPClient
	}
	return client
}

// CoordinatorHTTPClient returns an mTLS-configured HTTP client for coordinator calls.
func (registry *DeviceRegistry) CoordinatorHTTPClient() *http.Client {
	if registry == nil {
		return coordinatorHTTPClient
	}
	registry.tlsMutex.RLock()
	client := registry.coordClient
	registry.tlsMutex.RUnlock()
	if client == nil {
		return coordinatorHTTPClient
	}
	return client
}

// bootstrapTLSHTTPClient builds a short-lived HTTPS client for /devices/register
// or /ping that verifies the Coordinator CA fingerprint during handshake.
func bootstrapTLSHTTPClient(expectedFingerprint string, timeout time.Duration, pinnedCertOut **x509.Certificate) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				ServerName:         bootstrapServerName,
				InsecureSkipVerify: true, // Verified via VerifyPeerCertificate pinning coordinator CA fingerprint.
				VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
					if len(rawCerts) == 0 {
						return fmt.Errorf("peer presented no TLS certificate")
					}
					certs := make([]*x509.Certificate, 0, len(rawCerts))
					for _, raw := range rawCerts {
						parsed, err := x509.ParseCertificate(raw)
						if err != nil {
							return fmt.Errorf("parse peer certificate: %w", err)
						}
						certs = append(certs, parsed)
					}
					leaf := certs[0]
					now := time.Now()
					if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
						return fmt.Errorf("peer certificate is outside validity period")
					}
					var rootCA *x509.Certificate
					for _, candidate := range certs {
						if candidate.IsCA {
							rootCA = candidate
							break
						}
					}
					if rootCA == nil && leaf.IsCA {
						rootCA = leaf
					}
					if rootCA == nil {
						if expectedFingerprint != "" {
							return fmt.Errorf("coordinator did not present CA certificate for fingerprint verification")
						}
						return nil
					}
					if err := verifyLeafMatchesCA(leaf, rootCA, now); err != nil {
						return fmt.Errorf("coordinator certificate chain invalid: %w", err)
					}
					actualFP := certFingerprintSHA256(rootCA)
					if expectedFingerprint != "" && actualFP != expectedFingerprint {
						return &coordinatorError{
							Code:       coordinatorErrorIdentityChanged,
							Message:    "coordinator CA fingerprint changed; enter a new pairing code to trust it",
							StatusCode: http.StatusUnauthorized,
						}
					}
					if pinnedCertOut != nil {
						*pinnedCertOut = rootCA
					}
					return nil
				},
			},
		},
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// authenticatePeerMTLS verifies that an incoming peer request presented a valid
// Coordinator-signed client certificate belonging to an active, non-revoked device.
func authenticatePeerMTLS(registry *DeviceRegistry, request *http.Request, expectedDeviceID string) (string, error) {
	if registry == nil {
		return "", fmt.Errorf("device registry unavailable")
	}
	certDeviceID, ok := VerifiedPeerDeviceID(request)
	if !ok {
		return "", fmt.Errorf("valid mutual TLS client certificate required")
	}
	if expectedDeviceID != "" && certDeviceID != expectedDeviceID {
		return "", fmt.Errorf("sender device identity does not match authenticated mTLS certificate")
	}
	if !registry.IsDeviceAuthorized(certDeviceID) {
		return "", fmt.Errorf("device %s is not authorized or has been revoked", certDeviceID)
	}
	return certDeviceID, nil
}

// CAFingerprint returns the SHA-256 hex fingerprint of the trusted Coordinator CA.
func (registry *DeviceRegistry) CAFingerprint() string {
	if registry == nil {
		return ""
	}
	registry.tlsMutex.RLock()
	fp := registry.caFingerprint
	registry.tlsMutex.RUnlock()
	return fp
}
