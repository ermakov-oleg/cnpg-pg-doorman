// Package binaries serves the pg_doorman binaries baked into the plugin
// image so sidecar wrappers can upgrade the pooler in place. Integrity is
// guaranteed by the sha256 digests published in the rendered config Secret;
// TLS (the plugin server certificate) protects the transport.
package binaries

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/wrapper"
)

// DefaultDir is where the plugin image carries per-arch pg_doorman binaries.
const DefaultDir = "/binaries"

// LoadManifest hashes every <dir>/<arch>/pg_doorman. A missing dir returns
// nil, nil: binary delivery is simply disabled (e.g. ad-hoc dev builds).
func LoadManifest(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	manifest := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "pg_doorman")
		sum, err := wrapper.FileSHA256(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("hashing %s: %w", path, err)
		}
		manifest[e.Name()] = sum
	}
	if len(manifest) == 0 {
		return nil, nil
	}
	return manifest, nil
}

// Server serves GET /binaries/{arch} over TLS. It runs on every replica
// (NeedLeaderElection false): any plugin pod behind the Service can serve
// the download.
type Server struct {
	Dir      string
	Addr     string
	CertPath string
	KeyPath  string
}

func (s *Server) NeedLeaderElection() bool { return false }

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /binaries/{arch}", func(w http.ResponseWriter, r *http.Request) {
		arch := r.PathValue("arch")
		if strings.ContainsAny(arch, "/\\.") {
			http.NotFound(w, r)
			return
		}
		//nolint:gosec // arch is validated to not contain "/" or backslash
		http.ServeFile(w, r, filepath.Join(s.Dir, arch, "pg_doorman"))
	})
	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			// Reload per handshake: cert-manager rotates the serving pair.
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				cert, err := tls.LoadX509KeyPair(s.CertPath, s.KeyPath)
				if err != nil {
					return nil, err
				}
				return &cert, nil
			},
		},
	}
	//nolint:gosec // shutdown uses Background to avoid request context interference
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
