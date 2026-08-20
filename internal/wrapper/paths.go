package wrapper

// Paths shared between the rendering controller (which writes them into the
// config) and the wrapper (which materializes them inside the pod).
const (
	// ConfigSourcePath is the rendered config mounted from the per-cluster Secret.
	ConfigSourcePath = "/etc/pg-doorman-config/pg_doorman.yaml"
	// RuntimeConfigPath is the validated working copy pg_doorman runs on.
	RuntimeConfigPath = "/tmp/pg_doorman.yaml"
	// TLSCertPath is the CNPG server certificate mounted into the sidecar.
	TLSCertPath = "/etc/pg-doorman-tls/tls.crt"
	// RawTLSKeyPath is the mounted private key (SEC1 EC from CNPG).
	RawTLSKeyPath = "/etc/pg-doorman-tls/tls.key"
	// ConvertedTLSKeyPath is the PKCS#8 copy pg_doorman actually accepts.
	ConvertedTLSKeyPath = "/tmp/pg_doorman-tls.key"
	// RuntimeBinaryDir holds the pg_doorman binary the wrapper actually runs.
	// It lives on the tmpfs scratch volume: the root filesystem is read-only,
	// and the upstream binary upgrade re-executes argv[0], so the path must be
	// replaceable at runtime.
	RuntimeBinaryDir = "/tmp/bin"
	// RuntimeBinaryPath is argv[0] of the supervised pg_doorman process.
	RuntimeBinaryPath = RuntimeBinaryDir + "/pg_doorman"
	// ImageBinaryPath is the pg_doorman binary baked into the sidecar image,
	// used as the seed copy and as the fallback when delivery is unavailable.
	ImageBinaryPath = "/usr/bin/pg_doorman"
)
