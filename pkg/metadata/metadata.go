package metadata

import (
	"github.com/cloudnative-pg/cnpg-i/pkg/identity"
)

const PluginName = "pg-doorman.cnpg.io"

var Data = identity.GetPluginMetadataResponse{
	Name:          PluginName,
	Version:       "0.1.0",
	DisplayName:   "PgDoorman",
	ProjectUrl:    "https://github.com/o-ermakov/cnpg-pg-doorman",
	RepositoryUrl: "https://github.com/o-ermakov/cnpg-pg-doorman",
	License:       "Apache-2.0",
	LicenseUrl:    "https://github.com/o-ermakov/cnpg-pg-doorman/blob/main/LICENSE",
	Maturity:      "alpha",
}
