package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"onlineclipboard/server/internal/canon"
	"onlineclipboard/server/internal/config"
	"onlineclipboard/server/internal/extauth"
	"onlineclipboard/server/internal/httpapi"
	"onlineclipboard/server/internal/mailer"
	"onlineclipboard/server/internal/migrate"
	"onlineclipboard/server/internal/notify"
	"onlineclipboard/server/internal/passwd"
	"onlineclipboard/server/internal/store"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "check the container-local process health")
	flag.Parse()
	if *healthcheck {
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://127.0.0.1:8080/healthz")
		if err != nil {
			os.Exit(1)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}

	args := flag.Args()
	if len(args) == 0 {
		if err := runServer(); err != nil {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
		return
	}
	switch args[0] {
	case "migrate":
		if err := runMigrate(); err != nil {
			slog.Error("migrate failed", "error", err)
			os.Exit(1)
		}
	case "admin":
		if err := runAdmin(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", args[0])
		os.Exit(2)
	}
}

func runServer() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, st, hub, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	go st.RunJobs(ctx)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(cfg, st, hub, true),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	failures := make(chan error, 1)
	go func() { failures <- server.ListenAndServe() }()
	slog.Info("clipd ready", "address", cfg.HTTPAddr, "registration", cfg.RegistrationMode)
	select {
	case err := <-failures:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
	return nil
}

func runMigrate() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		return err
	}
	version, err := migrate.CurrentVersion(ctx, pool)
	if err != nil {
		return err
	}
	slog.Info("migrations complete", "version", version)
	return nil
}

func runAdmin(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: clipd admin invite|reset-password|rotate-sync-epoch")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, st, _, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	switch args[0] {
	case "invite":
		ttl := 24 * time.Hour
		token, exp, err := st.CreateInvite(ctx, ttl)
		if err != nil {
			return err
		}
		fmt.Printf("invite_token=%s\nexpires_at=%s\n", token, exp.Format(time.RFC3339))
		return nil
	case "reset-password":
		username := ""
		for i := 1; i < len(args); i++ {
			if args[i] == "--username" && i+1 < len(args) {
				username = args[i+1]
			}
		}
		if err := canon.Username(username); err != nil {
			return fmt.Errorf("need --username")
		}
		fmt.Fprint(os.Stderr, "new password: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return err
		}
		password := strings.TrimRight(line, "\r\n")
		if err := canon.Password(password); err != nil {
			return err
		}
		if _, err := passwd.Hash(password); err != nil {
			return err
		}
		return st.ResetPassword(ctx, username, password)
	case "rotate-sync-epoch":
		epoch, err := st.RotateSyncEpoch(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("sync_epoch=%s\n", epoch)
		return nil
	default:
		return fmt.Errorf("unknown admin command")
	}
}

func openStore(ctx context.Context, cfg config.Config) (*pgxpool.Pool, *store.Store, *notify.Hub, error) {
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, nil, fmt.Errorf("database: %w", err)
	}
	if err := migrate.Apply(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, nil, err
	}
	hub := notify.New()
	st := store.New(pool, cfg, hub)
	st.Mailer = mailer.FromConfig(cfg)
	if cfg.RegistrationMode == "external" {
		st.External = extauth.New(cfg.ExternalAuthURL)
	}
	return pool, st, hub, nil
}
