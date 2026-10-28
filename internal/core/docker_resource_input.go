package core

// EncryptDockerResourceInput keeps content only in an encrypted, queued
// operation. The worker removes ciphertext from the row at terminal status.
func (c *Core) EncryptDockerResourceInput(operationID string, raw []byte) ([]byte, error) {
	return c.keyring.Encrypt(raw, []byte("deployment-operation:"+operationID))
}
