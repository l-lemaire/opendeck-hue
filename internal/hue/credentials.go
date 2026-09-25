package hue

import (
	"encoding/json"
	"fmt"

	"github.com/l-lemaire/opendeck-hue/internal/secrets"
)

// Credentials are stored as one JSON blob per bridge in the secret store,
// under the bridge id. One entry per bridge keeps `auth forget` simple and
// lets several bridges coexist.

// SaveCredentials writes the credentials for bridgeID into the store.
func SaveCredentials(store secrets.Store, bridgeID string, creds Credentials) error {
	encoded, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	return store.Set(bridgeID, string(encoded))
}

// LoadCredentials reads the credentials for bridgeID. It returns
// secrets.ErrNotFound (wrapped) when the bridge was never paired.
func LoadCredentials(store secrets.Store, bridgeID string) (Credentials, error) {
	raw, err := store.Get(bridgeID)
	if err != nil {
		return Credentials{}, fmt.Errorf("credentials for bridge %s: %w", bridgeID, err)
	}
	var creds Credentials
	if err := json.Unmarshal([]byte(raw), &creds); err != nil {
		return Credentials{}, fmt.Errorf("credentials for bridge %s are corrupt: %w", bridgeID, err)
	}
	return creds, nil
}
