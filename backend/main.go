// Roost — a Go + TypeScript port of the Pterodactyl game server management
// panel. One binary, one SQLite file, wings-compatible APIs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"roost/internal/api"
	"roost/internal/seed"
	"roost/internal/store"
	"roost/internal/tlsmgr"
	"roost/web"
)

var version = "dev"

func main() {
	addr := flag.String("addr", envOr("ROOST_ADDR", ":8090"), "listen address")
	dbPath := flag.String("db", envOr("ROOST_DB", "roost.db"), "path to the SQLite database")
	staticDir := flag.String("static", envOr("ROOST_STATIC", ""), "serve frontend from this directory instead of the embedded assets")
	panelURL := flag.String("url", envOr("ROOST_URL", ""), "public URL of this panel (used in wings tokens)")
	dbviewerStatic := flag.String("dbviewer-static", envOr("ROOST_DBVIEWER_STATIC", ""), "serve the database viewer from this directory instead of the embedded assets")
	dbAllowHosts := flag.String("dbviewer-allow-hosts", envOr("ROOST_DBVIEWER_ALLOW_HOSTS", ""), "comma-separated allowlist of database hosts the viewer may connect to (empty = any)")
	httpsAddr := flag.String("https-addr", envOr("ROOST_HTTPS_ADDR", ":443"), "HTTPS listen address, used when automatic HTTPS is enabled")
	acmeCache := flag.String("acme-cache", envOr("ROOST_ACME_CACHE", "certs"), "directory where Let's Encrypt certificates are cached")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("Roost %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	if *panelURL != "" {
		st.SetSetting("app:url", *panelURL)
	}

	a := api.New(st)
	if err := seed.Run(a, st); err != nil {
		log.Fatalf("seed: %v", err)
	}

	viewer := api.NewDBViewer(2*time.Hour, splitCSV(*dbAllowHosts))
	defer viewer.Close()

	mux := a.Mux()
	a.MountDBViewer(mux, viewer, web.DBViewerHandler(*dbviewerStatic))
	mux.Handle("/", web.Handler(*staticDir))
	handler := a.WrapExternal(mux)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a.StartScheduler(ctx)

	// Automatic HTTPS: when configured, obtain and renew a Let's Encrypt
	// certificate for the panel's domain. The plaintext listener keeps serving
	// (so a local/LAN address still works) but also answers ACME HTTP-01
	// challenges.
	plainHandler := handler
	var httpsSrv *http.Server
	if tlsCfg := a.TLSSettings(); tlsCfg.Enabled && tlsCfg.Domain != "" {
		mgr := tlsmgr.New(tlsmgr.Config{
			Domain:   tlsCfg.Domain,
			Email:    tlsCfg.Email,
			CacheDir: *acmeCache,
			Staging:  tlsCfg.Staging,
		})
		a.SetTLSManager(mgr)
		plainHandler = mgr.HTTPHandler(handler)

		httpsSrv = &http.Server{
			Addr:              *httpsAddr,
			Handler:           handler,
			TLSConfig:         mgr.TLSConfig(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			log.Printf("automatic HTTPS enabled for %s (staging=%v, cache: %s)", tlsCfg.Domain, tlsCfg.Staging, *acmeCache)
			if err := httpsSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("HTTPS listener on %s failed: %v", *httpsAddr, err)
				log.Printf("  (binding :443 needs root or: setcap cap_net_bind_service=+ep ./roost)")
			}
		}()

		// Let's Encrypt always validates HTTP-01 against port 80, so serve the
		// challenge there too when the panel listens elsewhere.
		if *addr != ":80" {
			go func() {
				challenge := &http.Server{
					Addr:              ":80",
					Handler:           mgr.HTTPHandler(nil), // challenge, else redirect to HTTPS
					ReadHeaderTimeout: 10 * time.Second,
				}
				if err := challenge.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Printf("ACME challenge listener on :80 failed: %v", err)
					log.Printf("  (Let's Encrypt validates over port 80; certificate issuance will not succeed)")
				}
			}()
		}
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           plainHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
		if httpsSrv != nil {
			httpsSrv.Shutdown(shutdownCtx)
		}
	}()

	log.Printf("Roost %s listening on %s (db: %s)", version, *addr, *dbPath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

// splitCSV parses a comma-separated flag into trimmed, non-empty entries.
func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
