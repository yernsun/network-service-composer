package core

import "github.com/yernsun/network-service-composer/internal/volumemanager"

func (c *Core) Volumes() volumemanager.Manager {
	return volumemanager.New(c.store, c.config, c.keyring)
}
