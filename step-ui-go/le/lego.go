package le

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"
)

// issueMu serializes all IssueCert calls.  Concurrent issuance races on:
//   - loadOrCreateKey / saveRegistration (shared account key file);
//   - os.Setenv (process-global environment, fixed below by using Config.AuthToken);
//   - the ACME client state machine itself.
//
// A single-writer mutex is the simplest correct fix; ACME issuance is slow
// (~seconds) so queueing is acceptable.
var issueMu sync.Mutex

const (
	LEDirectory    = "/opt/step-ui/le-certs"
	LEAccountFile  = "/opt/step-ui/le-certs/account.json"
	LEKeyFile      = "/opt/step-ui/le-certs/account.key"
	LEProductionCA = "https://acme-v02.api.letsencrypt.org/directory"
	LEStagingCA    = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// LEUser реализует интерфейс registration.User
type LEUser struct {
	Email        string
	Registration *registration.Resource
	key          crypto.PrivateKey
}

func (u *LEUser) GetEmail() string                        { return u.Email }
func (u *LEUser) GetRegistration() *registration.Resource { return u.Registration }
func (u *LEUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// LEConfig конфигурация для выпуска
type LEConfig struct {
	Email     string
	Domain    string
	Provider  string // http01, cloudflare, route53, manual
	CFToken   string
	CFZoneID  string
	R53KeyID  string
	R53Secret string
	R53Region string
	Staging   bool
}

// LEResult результат выпуска
type LEResult struct {
	CertPath  string
	KeyPath   string
	IssuedAt  *time.Time
	ExpiresAt *time.Time
}

// IssueCert выпускает сертификат Let's Encrypt.
// It is safe to call concurrently; a package-level mutex serializes access to
// shared state (account key file, registration file, and the ACME client).
func IssueCert(cfg LEConfig) (*LEResult, error) {
	issueMu.Lock()
	defer issueMu.Unlock()

	if err := os.MkdirAll(filepath.Join(LEDirectory, cfg.Domain), 0o700); err != nil {
		return nil, fmt.Errorf("creating cert directory: %w", err)
	}

	// Загружаем или создаём ключ аккаунта (protected by issueMu)
	privateKey, err := loadOrCreateKey(LEKeyFile)
	if err != nil {
		return nil, fmt.Errorf("ключ аккаунта: %w", err)
	}

	user := &LEUser{Email: cfg.Email, key: privateKey}

	// Загружаем регистрацию если есть (protected by issueMu)
	if reg, err := loadRegistration(LEAccountFile); err == nil {
		user.Registration = reg
	}

	// Выбираем CA
	caURL := LEProductionCA
	if cfg.Staging {
		caURL = LEStagingCA
	}

	// Создаём LEGO клиент
	legoConfig := lego.NewConfig(user)
	legoConfig.CADirURL = caURL
	legoConfig.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(legoConfig)
	if err != nil {
		return nil, fmt.Errorf("lego client: %w", err)
	}

	// Настраиваем challenge провайдер
	switch cfg.Provider {
	case "http01":
		if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer("", "80")); err != nil {
			return nil, fmt.Errorf("setting http01 provider: %w", err)
		}
	case "cloudflare":
		if cfg.CFToken == "" {
			return nil, fmt.Errorf("Cloudflare API token не задан")
		}
		// Pass the token via Config.AuthToken instead of os.Setenv to avoid
		// the process-global environment mutation that races under concurrent
		// issuance (H-lego-race).
		cfCfg := cloudflare.NewDefaultConfig()
		cfCfg.AuthToken = cfg.CFToken
		cp, err := cloudflare.NewDNSProviderConfig(cfCfg)
		if err != nil {
			return nil, fmt.Errorf("cloudflare provider: %w", err)
		}
		if err := client.Challenge.SetDNS01Provider(cp); err != nil {
			return nil, fmt.Errorf("setting dns01 provider: %w", err)
		}
	default:
		return nil, fmt.Errorf("неизвестный провайдер: %s", cfg.Provider)
	}

	// Регистрация если нет
	if user.Registration == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, fmt.Errorf("регистрация: %w", err)
		}
		user.Registration = reg
		saveRegistration(LEAccountFile, reg)
	}

	// Запрашиваем сертификат
	request := certificate.ObtainRequest{
		Domains: []string{cfg.Domain},
		Bundle:  true,
	}
	certs, err := client.Certificate.Obtain(request)
	if err != nil {
		return nil, fmt.Errorf("выпуск сертификата: %w", err)
	}

	// Сохраняем файлы
	certDir := filepath.Join(LEDirectory, cfg.Domain)
	certPath := filepath.Join(certDir, "certificate.crt")
	keyPath := filepath.Join(certDir, "private.key")

	if err := os.WriteFile(certPath, certs.Certificate, 0o644); err != nil { //nolint:gosec // G306: certificates are world-readable by design
		return nil, err
	}
	if err := os.WriteFile(keyPath, certs.PrivateKey, 0o600); err != nil {
		return nil, err
	}

	// Парсим даты
	issued, expires := parseCertDates(certs.Certificate)

	return &LEResult{
		CertPath:  certPath,
		KeyPath:   keyPath,
		IssuedAt:  issued,
		ExpiresAt: expires,
	}, nil
}

func parseCertDates(certPEM []byte) (issued, expires *time.Time) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return issued, expires
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return issued, expires
	}
	i := cert.NotBefore
	e := cert.NotAfter
	return &i, &e
}

func loadOrCreateKey(path string) (crypto.PrivateKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			return x509.ParseECPrivateKey(block.Bytes)
		}
	}
	// Создаём новый ключ
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	data, _ := x509.MarshalECPrivateKey(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating key directory: %w", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: data}), 0o600); err != nil {
		return nil, fmt.Errorf("saving account key: %w", err)
	}
	return key, nil
}

type savedRegistration struct {
	Body *registration.Resource
}

func saveRegistration(path string, reg *registration.Resource) {
	data, _ := json.Marshal(&savedRegistration{Body: reg})
	// best-effort: failure to save registration does not abort the certificate issuance
	_ = os.WriteFile(path, data, 0o600)
}

func loadRegistration(path string) (*registration.Resource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s savedRegistration
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return s.Body, nil
}
