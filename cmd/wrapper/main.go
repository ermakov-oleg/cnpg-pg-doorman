package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/configgen"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/credentials"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/extclient"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/wrapper"
)

const (
	runtimeConfig   = "/tmp/pg_doorman.yaml"
	pollIntervalSec = 5
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	configName := os.Getenv("PG_DOORMAN_CONFIG_NAME")
	namespace := os.Getenv("PG_DOORMAN_CONFIG_NAMESPACE")
	poolerPort := envInt("POOLER_PORT", 6432, logger)
	metricsPort := envInt("METRICS_PORT", 9127, logger)

	if configName == "" {
		logger.Error("PG_DOORMAN_CONFIG_NAME is required")
		os.Exit(1)
	}
	if namespace == "" {
		logger.Error("PG_DOORMAN_CONFIG_NAMESPACE is required")
		os.Exit(1)
	}

	// Create k8s client with DisableFor (no informers) + ExtendedClient TTL cache
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{
					&corev1.Secret{},
					&v1alpha1.PgDoorman{},
				},
			},
		},
	})
	if err != nil {
		logger.Error("unable to create manager", "error", err)
		os.Exit(1)
	}

	// Start manager in background
	go func() {
		if err := mgr.Start(ctx); err != nil {
			logger.Error("manager failed", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for cache to sync (no-op for DisableFor types, but needed for manager readiness)
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		logger.Error("cache sync failed")
		os.Exit(1)
	}

	cl := extclient.NewExtendedClient(mgr.GetClient())

	// Build the config generator callback (resolves secrets + generates YAML)
	generate := makeConfigGenerator(cl, namespace, poolerPort, metricsPort)

	// Build the secret hash function for detecting secret rotation
	secretHashFn := func(ctx context.Context, spec *v1alpha1.PgDoormanSpec, ns string) (string, error) {
		return credentials.CollectSecretVersions(ctx, cl, spec, ns)
	}

	testConfig := wrapper.NewBinaryConfigTester(wrapper.PgDoormanBinary)

	// Generate initial config
	var initialCfg wrapper.InitialConfig
	gen, secretHash, err := generateAndWriteConfig(ctx, cl, configName, namespace, generate, secretHashFn, testConfig, logger)
	if err != nil {
		logger.Error("initial config generation failed, waiting", "error", err)
		initialCfg = wrapper.WaitForCRDConfig(ctx, cl, configName, namespace, runtimeConfig, generate, secretHashFn, testConfig, pollIntervalSec, logger)
		if ctx.Err() != nil {
			os.Exit(0)
		}
	} else {
		initialCfg = wrapper.InitialConfig{Generation: gen, SecretHash: secretHash}
	}

	// Start pg_doorman with restart
	proc := wrapper.NewProcess(runtimeConfig, logger)
	procDone := make(chan struct{})
	go func() {
		defer close(procDone)
		if err := proc.RunWithRestart(ctx); err != nil && ctx.Err() == nil {
			logger.Error("pg_doorman run failed", "error", err)
			os.Exit(1)
		}
	}()

	// Watch for CRD changes
	w := wrapper.NewCRDWatcher(cl, configName, namespace, runtimeConfig, proc, generate, secretHashFn, logger, initialCfg.Generation, initialCfg.SecretHash)
	go w.Run(ctx, pollIntervalSec)

	<-ctx.Done()
	logger.Info("shutting down, waiting for pg_doorman to exit")
	// The wrapper is PID 1: exiting main kills the container along with pg_doorman,
	// so wait until RunWithRestart observes the process exit.
	<-procDone
}

func makeConfigGenerator(cl client.Client, namespace string, poolerPort, metricsPort int) wrapper.ConfigGenerator {
	return func(ctx context.Context, spec *v1alpha1.PgDoormanSpec) ([]byte, error) {
		passwords, err := credentials.ResolvePasswords(ctx, cl, namespace, spec)
		if err != nil {
			return nil, err
		}
		return configgen.Generate(spec, poolerPort, metricsPort, passwords)
	}
}

func generateAndWriteConfig(
	ctx context.Context,
	cl client.Client,
	configName, namespace string,
	generate wrapper.ConfigGenerator,
	secretHashFn wrapper.SecretHashFunc,
	testConfig wrapper.ConfigTester,
	logger *slog.Logger,
) (int64, string, error) {
	var pgDoorman v1alpha1.PgDoorman
	if err := cl.Get(ctx, client.ObjectKey{Name: configName, Namespace: namespace}, &pgDoorman); err != nil {
		return 0, "", err
	}

	data, err := generate(ctx, &pgDoorman.Spec)
	if err != nil {
		return 0, "", err
	}

	if _, err := wrapper.ValidateConfigBytes(data); err != nil {
		return 0, "", err
	}

	candidate := runtimeConfig + wrapper.CandidateSuffix
	if err := wrapper.AtomicWrite(candidate, data); err != nil {
		return 0, "", err
	}
	if err := testConfig(ctx, candidate); err != nil {
		_ = os.Remove(candidate)
		return 0, "", err
	}
	if err := os.Rename(candidate, runtimeConfig); err != nil {
		return 0, "", err
	}

	var secretHash string
	if secretHashFn != nil {
		secretHash, _ = secretHashFn(ctx, &pgDoorman.Spec, namespace)
	}

	logger.Info("config generated and written", "configName", configName)
	return pgDoorman.Generation, secretHash, nil
}

func envInt(key string, defaultVal int, logger *slog.Logger) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		logger.Warn("invalid integer env var, using default", "key", key, "value", v, "default", defaultVal, "error", err)
		return defaultVal
	}
	return i
}
