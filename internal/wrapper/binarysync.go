package wrapper

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	downloadTimeout = 2 * time.Minute
	// idleConnTimeout bounds how long an unused connection (and its goroutines)
	// survives: every download builds its own transport from the spec CA bundle.
	idleConnTimeout = 30 * time.Second
)

// maxBinaryBytes caps what a download may write into the tmpfs runtime dir:
// pg_doorman is ~11MB, so anything near this size is a hostile or broken
// endpoint, not a binary. A var so tests can shrink it.
var maxBinaryBytes int64 = 256 << 20

// BinarySyncer materializes the desired pg_doorman binary at the runtime
// path the wrapper executes. Sources, in order of preference: the already
// installed runtime binary, the image binary, the plugin delivery endpoint.
type BinarySyncer struct {
	specPath    string
	imagePath   string
	runtimePath string
	arch        string
	logger      *slog.Logger
}

func NewBinarySyncer(specPath, imagePath, runtimePath, arch string, logger *slog.Logger) *BinarySyncer {
	return &BinarySyncer{
		specPath:    specPath,
		imagePath:   imagePath,
		runtimePath: runtimePath,
		arch:        arch,
		logger:      logger,
	}
}

// EnsureAtStartup installs the desired binary before pg_doorman starts.
// Failures to download degrade to the image binary: serving traffic on the
// baked-in version beats crash-looping the pooler.
//
// The returned bytes are the BinaryWatcher seed: the binary.json contents only
// when the desired state was reached, nil when startup ended on the fallback
// binary. A nil seed makes the watcher treat the mounted spec as new on its
// first tick and retry it, instead of leaving the pod stale until the next
// plugin release changes the file.
func (s *BinarySyncer) EnsureAtStartup(ctx context.Context) ([]byte, error) {
	data, err := os.ReadFile(s.specPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		data = nil
	}

	var spec *BinarySpec
	if data != nil {
		if spec, err = ParseBinarySpec(data); err != nil {
			s.logger.Error("invalid binary spec, using image binary", "error", err)
			spec = nil
		}
	}

	desired := ""
	if spec != nil {
		desired = spec.SHA256[s.arch]
	}
	if desired == "" {
		if spec != nil {
			s.logger.Warn("binary spec has no digest for this arch, using image binary", "arch", s.arch)
		}
		return data, s.installFromImage("")
	}

	if cur, err := FileSHA256(s.runtimePath); err == nil && cur == desired {
		return data, nil
	}
	if img, err := FileSHA256(s.imagePath); err == nil && img == desired {
		return data, s.installFromImage(desired)
	}

	if err := s.Download(ctx, spec, desired, s.runtimePath); err != nil {
		s.logger.Error("binary download failed, falling back to image binary", "error", err)
		BinaryStale.Set(1)
		return nil, s.installFromImage("")
	}
	s.logger.Info("desired pg_doorman binary installed before start", "sha256", desired)
	BinaryStale.Set(0)
	return data, nil
}

// RevertToImage puts the image binary back at the runtime path. It reports
// whether anything changed: false means the runtime binary already is the image
// one, so there is nothing left to fall back to.
func (s *BinarySyncer) RevertToImage() (bool, error) {
	imgSHA, err := FileSHA256(s.imagePath)
	if err != nil {
		return false, fmt.Errorf("hashing image binary: %w", err)
	}
	if cur, err := FileSHA256(s.runtimePath); err == nil && cur == imgSHA {
		return false, nil
	}
	if err := s.installFromImage(""); err != nil {
		return false, err
	}
	return true, nil
}

// installFromImage copies the image binary to the runtime path unless it is
// already identical. wantSHA, when set, double-checks the copy.
func (s *BinarySyncer) installFromImage(wantSHA string) error {
	imgSHA, err := FileSHA256(s.imagePath)
	if err != nil {
		return fmt.Errorf("hashing image binary: %w", err)
	}
	if wantSHA != "" && imgSHA != wantSHA {
		return fmt.Errorf("image binary digest mismatch: got %s want %s", imgSHA, wantSHA)
	}
	if cur, err := FileSHA256(s.runtimePath); err == nil && cur == imgSHA {
		return nil
	}
	src, err := os.Open(s.imagePath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	return atomicInstall(s.runtimePath, src)
}

// Download fetches the binary for s.arch, verifies wantSHA and atomically
// installs it at destPath with mode 0755.
func (s *BinarySyncer) Download(ctx context.Context, spec *BinarySpec, wantSHA, destPath string) error {
	client, err := httpClientFor(spec)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL+"/binaries/"+s.arch, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binary endpoint returned %s", resp.Status)
	}
	if resp.ContentLength > maxBinaryBytes {
		return fmt.Errorf("binary endpoint declared %d bytes, over the %d byte limit", resp.ContentLength, maxBinaryBytes)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".pg_doorman-*")
	if err != nil {
		if mkErr := os.MkdirAll(filepath.Dir(destPath), 0o750); mkErr != nil {
			return mkErr
		}
		if tmp, err = os.CreateTemp(filepath.Dir(destPath), ".pg_doorman-*"); err != nil {
			return err
		}
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	h := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxBinaryBytes+1))
	if err != nil {
		return err
	}
	if written > maxBinaryBytes {
		return fmt.Errorf("binary exceeds the %d byte limit", maxBinaryBytes)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return fmt.Errorf("downloaded binary digest mismatch: got %s want %s", got, wantSHA)
	}
	if err := tmp.Chmod(0o755); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), destPath)
}

func httpClientFor(spec *BinarySpec) (*http.Client, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13}
	if spec.CABundle != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(spec.CABundle)) {
			return nil, fmt.Errorf("binary spec: cannot parse CA bundle")
		}
		tlsCfg.RootCAs = pool
	}
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: tlsCfg,
		IdleConnTimeout: idleConnTimeout,
	}}, nil
}

func atomicInstall(destPath string, src io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".pg_doorman-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), destPath)
}
