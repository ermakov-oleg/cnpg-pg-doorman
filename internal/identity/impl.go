package identity

import (
	"context"

	"github.com/cloudnative-pg/cnpg-i/pkg/identity"

	"github.com/ermakov-oleg/cnpg-pg-doorman/pkg/metadata"
)

type Implementation struct {
	identity.UnimplementedIdentityServer
}

func (i Implementation) GetPluginMetadata(
	_ context.Context,
	_ *identity.GetPluginMetadataRequest,
) (*identity.GetPluginMetadataResponse, error) {
	return &metadata.Data, nil
}

func (i Implementation) GetPluginCapabilities(
	_ context.Context,
	_ *identity.GetPluginCapabilitiesRequest,
) (*identity.GetPluginCapabilitiesResponse, error) {
	return &identity.GetPluginCapabilitiesResponse{
		Capabilities: []*identity.PluginCapability{
			{
				Type: &identity.PluginCapability_Service_{
					Service: &identity.PluginCapability_Service{
						Type: identity.PluginCapability_Service_TYPE_LIFECYCLE_SERVICE,
					},
				},
			},
			{
				Type: &identity.PluginCapability_Service_{
					Service: &identity.PluginCapability_Service{
						Type: identity.PluginCapability_Service_TYPE_OPERATOR_SERVICE,
					},
				},
			},
			{
				Type: &identity.PluginCapability_Service_{
					Service: &identity.PluginCapability_Service{
						Type: identity.PluginCapability_Service_TYPE_RECONCILER_HOOKS,
					},
				},
			},
		},
	}, nil
}

func (i Implementation) Probe(
	_ context.Context,
	_ *identity.ProbeRequest,
) (*identity.ProbeResponse, error) {
	return &identity.ProbeResponse{Ready: true}, nil
}
