package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"sync"

	"onlineclipboard/server/internal/config"
)

type Sender interface {
	SendCode(ctx context.Context, email, code string) error
}

type LogSender struct {
	mu        sync.Mutex
	LastEmail string
	LastCode  string
	Codes     map[string]string
}

func (s *LogSender) SendCode(ctx context.Context, email, code string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Codes == nil {
		s.Codes = map[string]string{}
	}
	s.Codes[email] = code
	s.LastEmail = email
	s.LastCode = code
	slog.Warn("email verification code (log backend)", "email", email, "code", code)
	return nil
}

func (s *LogSender) Code(email string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Codes == nil {
		return ""
	}
	return s.Codes[email]
}

type SMTPSender struct {
	Cfg config.Config
}

func FromConfig(cfg config.Config) Sender {
	switch cfg.EmailBackend {
	case "log":
		return &LogSender{}
	case "smtp":
		return SMTPSender{Cfg: cfg}
	default:
		return nil
	}
}

func (s SMTPSender) SendCode(ctx context.Context, email, code string) error {
	_ = ctx
	subject := "云剪贴板注册验证码"
	body := "您的注册验证码是 " + code + "，15 分钟内有效。如果不是您本人操作，请忽略这封邮件。\n"
	return sendMail(s.Cfg, email, subject, body)
}

func sendMail(cfg config.Config, to, subject, body string) error {
	from := cfg.SMTPFrom
	msg := strings.Join([]string{
		"From: " + from,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")
	addr := net.JoinHostPort(cfg.SMTPHost, fmt.Sprintf("%d", cfg.SMTPPort))
	var auth smtp.Auth
	if cfg.SMTPUsername != "" {
		auth = smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPHost)
	}
	if cfg.SMTPSecure || cfg.SMTPPort == 465 {
		tlsCfg := &tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12}
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return err
		}
		defer conn.Close()
		client, err := smtp.NewClient(conn, cfg.SMTPHost)
		if err != nil {
			return err
		}
		defer client.Close()
		if auth != nil {
			if err := client.Auth(auth); err != nil {
				return err
			}
		}
		if err := client.Mail(from); err != nil {
			return err
		}
		if err := client.Rcpt(to); err != nil {
			return err
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(msg)); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		return client.Quit()
	}
	return smtp.SendMail(addr, auth, from, []string{to}, []byte(msg))
}
