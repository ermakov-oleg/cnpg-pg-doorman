package metadata

import (
	"github.com/cloudnative-pg/cnpg-i/pkg/identity"
)

const PluginName = "pg-doorman.cnpg.io"

var Data = identity.GetPluginMetadataResponse{
	Name:          PluginName,
	Version:       "0.4.0", // x-release-please-version
	DisplayName:   "PgDoorman",
	ProjectUrl:    "https://github.com/ermakov-oleg/cnpg-pg-doorman",
	RepositoryUrl: "https://github.com/ermakov-oleg/cnpg-pg-doorman",
	License:       "Apache-2.0",
	LicenseUrl:    "https://github.com/ermakov-oleg/cnpg-pg-doorman/blob/main/LICENSE",
	Maturity:      "alpha",
}
