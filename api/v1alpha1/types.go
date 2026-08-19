package v1alpha1

import (
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PgDoormanSpec defines the desired state of PgDoorman.
type PgDoormanSpec struct {
	// ClusterRef names the CNPG Cluster this configuration belongs to. It is
	// the source of truth for the Cluster<->PgDoorman relation (the
	// configName plugin parameter is only discovery), enabling ownership,
	// status aggregation and admission validation. Immutable.
	// +kubebuilder:validation:XValidation:rule="self.name == oldSelf.name",message="clusterRef is immutable"
	ClusterRef machineryapi.LocalObjectReference `json:"clusterRef"`

	// Pools defines the connection pools.
	// +kubebuilder:validation:MinProperties=1
	Pools map[string]PoolSpec `json:"pools"`

	// General settings for pg_doorman.
	// +optional
	General *GeneralSpec `json:"general,omitempty"`

	// Prometheus metrics configuration.
	// +optional
	Prometheus *PrometheusSpec `json:"prometheus,omitempty"`
}

// PoolSpec defines configuration for a single connection pool.
// +kubebuilder:validation:XValidation:rule="(has(self.users) && self.users.size() > 0) || has(self.authQuery)",message="pool must define users or authQuery"
type PoolSpec struct {
	// PoolMode is the pooling mode (default: "transaction").
	// +kubebuilder:validation:Enum=session;transaction
	// +kubebuilder:default=transaction
	// +optional
	PoolMode string `json:"poolMode,omitempty"`

	// DefaultPoolSize is the data pool size per auth_query user (default: 40).
	// Maps to auth_query.pool_size.
	// +kubebuilder:default=40
	// +kubebuilder:validation:Minimum=1
	// +optional
	DefaultPoolSize *int `json:"defaultPoolSize,omitempty"`

	// AuthQuery defines auth_query-based authentication.
	// +optional
	AuthQuery *AuthQuerySpec `json:"authQuery,omitempty"`

	// Users defines static user credentials.
	// +optional
	Users []UserSpec `json:"users,omitempty"`
}

// AuthQuerySpec defines auth_query configuration.
type AuthQuerySpec struct {
	// User is the PostgreSQL user for auth queries.
	// +kubebuilder:validation:MinLength=1
	User string `json:"user"`

	// Query is the SQL query (default: "SELECT * FROM public.doorman_auth_query($1)").
	// +kubebuilder:default="SELECT * FROM public.doorman_auth_query($1)"
	// +optional
	Query string `json:"query,omitempty"`

	// PasswordSecretRef references a Secret containing the auth user password.
	// If not set, empty password is used.
	// +optional
	PasswordSecretRef *machineryapi.SecretKeySelector `json:"passwordSecretRef,omitempty"`

	// Database is the database for auth queries (default: "postgres").
	// +kubebuilder:default=postgres
	// +optional
	Database string `json:"database,omitempty"`

	// PoolSize is the number of executor connections running auth queries (default: 2).
	// Maps to auth_query.workers.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=2
	// +optional
	PoolSize *int `json:"poolSize,omitempty"`
}

// UserSpec defines a static user.
type UserSpec struct {
	// Username is the PostgreSQL username.
	// +kubebuilder:validation:MinLength=1
	Username string `json:"username"`

	// PasswordSecretRef references a Secret containing the user password.
	PasswordSecretRef machineryapi.SecretKeySelector `json:"passwordSecretRef"`

	// PoolSize is the pool size for this user (default: 20).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=20
	// +optional
	PoolSize *int `json:"poolSize,omitempty"`
}

// GeneralSpec defines general pg_doorman settings.
type GeneralSpec struct {
	// MaxConnections is the maximum number of connections (default: 8192).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=8192
	// +optional
	MaxConnections *int `json:"maxConnections,omitempty"`

	// WorkerThreads is the number of worker threads (default: 4).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=4
	// +optional
	WorkerThreads *int `json:"workerThreads,omitempty"`

	// ConnectTimeout is the connection timeout (default: "3s").
	// A plain number is milliseconds; suffixes ms/s/m/h/d are supported.
	// +kubebuilder:validation:Pattern=`^[0-9]+(ms|s|m|h|d)?$`
	// +kubebuilder:default="3s"
	// +optional
	ConnectTimeout string `json:"connectTimeout,omitempty"`

	// IdleTimeout is the idle connection timeout (default: "5m").
	// A plain number is milliseconds; suffixes ms/s/m/h/d are supported.
	// +kubebuilder:validation:Pattern=`^[0-9]+(ms|s|m|h|d)?$`
	// +kubebuilder:default="5m"
	// +optional
	IdleTimeout string `json:"idleTimeout,omitempty"`

	// ServerLifetime is the server connection lifetime (default: "5m").
	// A plain number is milliseconds; suffixes ms/s/m/h/d are supported.
	// +kubebuilder:validation:Pattern=`^[0-9]+(ms|s|m|h|d)?$`
	// +kubebuilder:default="5m"
	// +optional
	ServerLifetime string `json:"serverLifetime,omitempty"`

	// ShutdownTimeout is the graceful shutdown timeout (default: "10s").
	// A plain number is milliseconds; suffixes ms/s/m/h/d are supported.
	// +kubebuilder:validation:Pattern=`^[0-9]+(ms|s|m|h|d)?$`
	// +kubebuilder:default="10s"
	// +optional
	ShutdownTimeout string `json:"shutdownTimeout,omitempty"`

	// AdminUsername is the admin user (default: "admin").
	// +kubebuilder:default=admin
	// +optional
	AdminUsername string `json:"adminUsername,omitempty"`

	// AdminPasswordSecretRef references a Secret containing the admin password.
	// There is deliberately no plaintext alternative: a password in the CR
	// would land in etcd, GitOps repos and manifest backups. When unset, the
	// wrapper generates a random per-pod password.
	// +optional
	AdminPasswordSecretRef *machineryapi.SecretKeySelector `json:"adminPasswordSecretRef,omitempty"`
}

// PrometheusSpec defines Prometheus metrics configuration.
type PrometheusSpec struct {
	// Enabled controls whether Prometheus metrics are enabled (default: true).
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// PgDoormanStatus defines the observed state of PgDoorman.
type PgDoormanStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:shortName=pgd
// +kubebuilder:printcolumn:name="Pools",type=string,JSONPath=`.spec.pools`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PgDoorman is the Schema for the pgdoormans API.
type PgDoorman struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec PgDoormanSpec `json:"spec"`
	// +optional
	Status PgDoormanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PgDoormanList contains a list of PgDoorman.
type PgDoormanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PgDoorman `json:"items"`
}
