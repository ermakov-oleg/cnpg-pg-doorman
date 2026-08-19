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
)
