package core

import (
	"strings"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/model"
)

func prepareApplicationNetwork(input *ApplicationPreflightInput, runtime model.Runtime, project, gatewayNetwork string) error {
	input.ProjectName = project
	name := strings.TrimSpace(input.NetworkName)
	if name == "" {
		name = input.ConfigurationSource.NetworkName
	}
	if runtime.Type != model.RuntimeSwarm {
		if name != "" {
			return controlInvalid("network_name", "application network names require a Swarm runtime")
		}
		return nil
	}
	if name == "" && input.ConfigurationSource.RestoredFrom == "" && input.ConfigurationSource.ServiceScope == nil {
		name = project
	}
	if name != "" {
		if err := applicationx.ValidateApplicationNetwork(name, gatewayNetwork, input.ComposeYAML); err != nil {
			return controlInvalid("network_name", err.Error())
		}
	}
	input.NetworkName = name
	input.ConfigurationSource.NetworkName = name
	return nil
}
