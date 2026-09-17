package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr              string
	PublicOrigin          string
	DatabaseURL           string
	RegistrationMode      string
	ExternalAuthURL       string
	ExternalRegisterURL   string
	EmailBackend          string
	SMTPHost              string
	SMTPPort              int
	SMTPUsername          string
	SMTPPassword          string
	SMTPFrom              string
	SMTPSecure            bool
	MaxTextBytes          int
	MaxRequestBytes       int
	MaxItems              int
	MaxCiphertextBytes    int64
	TrashRetentionSeconds int
	EventRetentionDays    int
	LogLevel              string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:              envOr("CLIP_HTTP_ADDR", "127.0.0.1:8080"),
		PublicOrigin:          strings.TrimSpace(os.Getenv("CLIP_PUBLIC_ORIGIN")),
		RegistrationMode:      envOr("CLIP_REGISTRATION_MODE", "invite"),
		ExternalAuthURL:       strings.TrimSpace(os.Getenv("CLIP_EXTERNAL_AUTH_URL")),
		ExternalRegisterURL:   strings.TrimSpace(os.Getenv("CLIP_EXTERNAL_REGISTER_URL")),
		EmailBackend:          strings.TrimSpace(os.Getenv("CLIP_EMAIL_BACKEND")),
		SMTPHost:              strings.TrimSpace(os.Getenv("CLIP_SMTP_HOST")),
		SMTPUsername:          strings.TrimSpace(os.Getenv("CLIP_SMTP_USERNAME")),
		SMTPPassword:          os.Getenv("CLIP_SMTP_PASSWORD"),
		SMTPFrom:              strings.TrimSpace(os.Getenv("CLIP_SMTP_FROM")),
		MaxTextBytes:          65536,
		MaxRequestBytes:       131072,
		MaxItems:              10000,
		MaxCiphertextBytes:    104857600,
		TrashRetentionSeconds: 604800,
		EventRetentionDays:    30,
		LogLevel:              envOr("CLIP_LOG_LEVEL", "info"),
	}
	if _, _, err := net.SplitHostPort(cfg.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("CLIP_HTTP_ADDR must be host:port: %w", err)
	}
	url, err := databaseURL()
	if err != nil {
		return Config{}, err
	}
	cfg.DatabaseURL = url
	switch cfg.RegistrationMode {
	case "invite", "disabled", "open", "email", "external":
	default:
		return Config{}, fmt.Errorf("CLIP_REGISTRATION_MODE must be invite, disabled, open, email or external")
	}
	if cfg.RegistrationMode == "external" {
		if cfg.ExternalAuthURL == "" {
			return Config{}, fmt.Errorf("CLIP_EXTERNAL_AUTH_URL is required when CLIP_REGISTRATION_MODE=external")
		}
		if _, err := parseHTTPURL(cfg.ExternalAuthURL); err != nil {
			return Config{}, fmt.Errorf("CLIP_EXTERNAL_AUTH_URL: %w", err)
		}
		if cfg.ExternalRegisterURL != "" {
			if _, err := parseHTTPURL(cfg.ExternalRegisterURL); err != nil {
				return Config{}, fmt.Errorf("CLIP_EXTERNAL_REGISTER_URL: %w", err)
			}
		}
	}
	if cfg.EmailBackend == "" {
		if cfg.SMTPHost != "" {
			cfg.EmailBackend = "smtp"
		} else if cfg.RegistrationMode == "email" {
			return Config{}, fmt.Errorf("email registration requires CLIP_SMTP_HOST or CLIP_EMAIL_BACKEND=log")
		}
	}
	switch cfg.EmailBackend {
	case "", "smtp", "log":
	default:
		return Config{}, fmt.Errorf("CLIP_EMAIL_BACKEND must be smtp or log")
	}
	if cfg.EmailBackend == "smtp" {
		if cfg.SMTPHost == "" {
			return Config{}, fmt.Errorf("CLIP_SMTP_HOST is required for smtp email backend")
		}
		if cfg.SMTPFrom == "" {
			cfg.SMTPFrom = cfg.SMTPUsername
		}
		if cfg.SMTPFrom == "" {
			return Config{}, fmt.Errorf("CLIP_SMTP_FROM or CLIP_SMTP_USERNAME is required for smtp")
		}
	}
	if v := strings.TrimSpace(os.Getenv("CLIP_SMTP_PORT")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return Config{}, fmt.Errorf("CLIP_SMTP_PORT must be 1-65535")
		}
		cfg.SMTPPort = n
	} else if cfg.EmailBackend == "smtp" {
		cfg.SMTPPort = 465
	}
	if v := strings.TrimSpace(os.Getenv("CLIP_SMTP_SECURE")); v != "" {
		cfg.SMTPSecure = v == "1" || strings.EqualFold(v, "true")
	} else {
		cfg.SMTPSecure = cfg.SMTPPort == 465
	}
	if v := os.Getenv("CLIP_MAX_TEXT_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65536 {
			return Config{}, fmt.Errorf("CLIP_MAX_TEXT_BYTES must be 1-65536")
		}
		cfg.MaxTextBytes = n
	}
	if v := os.Getenv("CLIP_MAX_ITEMS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("CLIP_MAX_ITEMS must be a positive integer")
		}
		cfg.MaxItems = n
	}
	if v := os.Getenv("CLIP_MAX_CIPHERTEXT_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("CLIP_MAX_CIPHERTEXT_BYTES must be a positive integer")
		}
		cfg.MaxCiphertextBytes = n
	}
	if v := os.Getenv("CLIP_EVENT_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("CLIP_EVENT_RETENTION_DAYS must be a positive integer")
		}
		cfg.EventRetentionDays = n
	}
	if v := os.Getenv("CLIP_TRASH_RETENTION_SECONDS"); v != "" && v != "604800" {
		return Config{}, fmt.Errorf("CLIP_TRASH_RETENTION_SECONDS is fixed at 604800 for protocol v1")
	}
	return cfg, nil
}

func (c Config) CanRegister() bool {
	switch c.RegistrationMode {
	case "invite", "open", "email":
		return true
	default:
		return false
	}
}

func (c Config) InviteRequired() bool {
	return c.RegistrationMode == "invite"
}

func (c Config) EmailVerification() bool {
	return c.RegistrationMode == "email"
}

func (c Config) MinPasswordChars() int {
	if c.RegistrationMode == "external" {
		return 1
	}
	return 12
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("must be http or https")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("missing host")
	}
	return u, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func databaseURL() (string, error) {
	if path := strings.TrimSpace(os.Getenv("CLIP_DATABASE_URL_FILE")); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read CLIP_DATABASE_URL_FILE: %w", err)
		}
		url := strings.TrimSpace(string(raw))
		if url == "" {
			return "", fmt.Errorf("CLIP_DATABASE_URL_FILE is empty")
		}
		return url, nil
	}
	if url := strings.TrimSpace(os.Getenv("CLIP_DATABASE_URL")); url != "" {
		return url, nil
	}
	return "", fmt.Errorf("CLIP_DATABASE_URL or CLIP_DATABASE_URL_FILE is required")
}

func RedactURL(url string) string {
	if i := strings.Index(url, "@"); i >= 0 {
		if j := strings.Index(url, "://"); j >= 0 && j < i {
			return url[:j+3] + "***" + url[i:]
		}
	}
	return url
}
