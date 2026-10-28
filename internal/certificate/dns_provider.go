package certificate

import (
	"fmt"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/hurricane"
	"github.com/go-acme/lego/v4/providers/dns/ovh"
	"github.com/go-acme/lego/v4/providers/dns/tencentcloud"

	"github.com/yernsun/network-service-composer/internal/config"
)

func newDNSProvider(profile config.DNSProviderProfile) (challenge.Provider, error) {
	credentials := profile.Credentials
	switch profile.Provider {
	case "alidns":
		providerConfig := alidns.NewDefaultConfig()
		providerConfig.APIKey = credentials.AccessKey
		providerConfig.SecretKey = credentials.SecretKey
		providerConfig.SecurityToken = credentials.SecurityToken
		providerConfig.RAMRole = credentials.RAMRole
		providerConfig.RegionID = profile.Region
		if profile.TTL > 0 {
			providerConfig.TTL = profile.TTL
		}
		if profile.PropagationTimeout.Std() > 0 {
			providerConfig.PropagationTimeout = profile.PropagationTimeout.Std()
		}
		if profile.PollingInterval.Std() > 0 {
			providerConfig.PollingInterval = profile.PollingInterval.Std()
		}
		if profile.HTTPTimeout.Std() > 0 {
			providerConfig.HTTPTimeout = profile.HTTPTimeout.Std()
		}
		provider, err := alidns.NewDNSProviderConfig(providerConfig)
		if err != nil {
			return nil, fmt.Errorf("initialize alidns provider: %w", err)
		}
		return provider, nil

	case "cloudflare":
		providerConfig := cloudflare.NewDefaultConfig()
		providerConfig.AuthToken = credentials.APIToken
		providerConfig.ZoneToken = credentials.ZoneAPIToken
		providerConfig.AuthEmail = credentials.Email
		providerConfig.AuthKey = credentials.APIKey
		providerConfig.BaseURL = profile.BaseURL
		if profile.TTL > 0 {
			providerConfig.TTL = profile.TTL
		}
		if profile.PropagationTimeout.Std() > 0 {
			providerConfig.PropagationTimeout = profile.PropagationTimeout.Std()
		}
		if profile.PollingInterval.Std() > 0 {
			providerConfig.PollingInterval = profile.PollingInterval.Std()
		}
		if profile.HTTPTimeout.Std() > 0 {
			providerConfig.HTTPClient.Timeout = profile.HTTPTimeout.Std()
		}
		provider, err := cloudflare.NewDNSProviderConfig(providerConfig)
		if err != nil {
			return nil, fmt.Errorf("initialize cloudflare provider: %w", err)
		}
		return provider, nil

	case "tencentcloud":
		providerConfig := tencentcloud.NewDefaultConfig()
		providerConfig.SecretID = credentials.SecretID
		providerConfig.SecretKey = credentials.SecretKey
		providerConfig.SessionToken = credentials.SessionToken
		providerConfig.Region = profile.Region
		if profile.TTL > 0 {
			providerConfig.TTL = profile.TTL
		}
		if profile.PropagationTimeout.Std() > 0 {
			providerConfig.PropagationTimeout = profile.PropagationTimeout.Std()
		}
		if profile.PollingInterval.Std() > 0 {
			providerConfig.PollingInterval = profile.PollingInterval.Std()
		}
		if profile.HTTPTimeout.Std() > 0 {
			providerConfig.HTTPTimeout = profile.HTTPTimeout.Std()
		}
		provider, err := tencentcloud.NewDNSProviderConfig(providerConfig)
		if err != nil {
			return nil, fmt.Errorf("initialize tencentcloud provider: %w", err)
		}
		return provider, nil

	case "hurricane":
		providerConfig := hurricane.NewDefaultConfig()
		providerConfig.Credentials = make(map[string]string, len(credentials.Tokens))
		for domain, token := range credentials.Tokens {
			providerConfig.Credentials[domain] = token
		}
		if profile.PropagationTimeout.Std() > 0 {
			providerConfig.PropagationTimeout = profile.PropagationTimeout.Std()
		}
		if profile.PollingInterval.Std() > 0 {
			providerConfig.PollingInterval = profile.PollingInterval.Std()
		}
		if profile.SequenceInterval.Std() > 0 {
			providerConfig.SequenceInterval = profile.SequenceInterval.Std()
		}
		if profile.HTTPTimeout.Std() > 0 {
			providerConfig.HTTPClient.Timeout = profile.HTTPTimeout.Std()
		}
		provider, err := hurricane.NewDNSProviderConfig(providerConfig)
		if err != nil {
			return nil, fmt.Errorf("initialize hurricane provider: %w", err)
		}
		return provider, nil

	case "ovh":
		providerConfig := ovh.NewDefaultConfig()
		providerConfig.APIEndpoint = profile.Region
		if providerConfig.APIEndpoint == "" {
			providerConfig.APIEndpoint = "ovh-eu"
		}
		providerConfig.ApplicationKey = credentials.ApplicationKey
		providerConfig.ApplicationSecret = credentials.ApplicationSecret
		providerConfig.ConsumerKey = credentials.ConsumerKey
		if profile.TTL > 0 {
			providerConfig.TTL = profile.TTL
		}
		if profile.PropagationTimeout.Std() > 0 {
			providerConfig.PropagationTimeout = profile.PropagationTimeout.Std()
		}
		if profile.PollingInterval.Std() > 0 {
			providerConfig.PollingInterval = profile.PollingInterval.Std()
		}
		if profile.HTTPTimeout.Std() > 0 {
			providerConfig.HTTPClient.Timeout = profile.HTTPTimeout.Std()
		}
		provider, err := ovh.NewDNSProviderConfig(providerConfig)
		if err != nil {
			return nil, fmt.Errorf("initialize ovh provider: %w", err)
		}
		return provider, nil

	default:
		return nil, fmt.Errorf("unsupported DNS provider %q", profile.Provider)
	}
}
