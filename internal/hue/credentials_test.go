package hue

import (
	"errors"
	"testing"

	"github.com/llemaire/streamdeck/internal/secrets"
)

// memStore is a throwaway in-memory Store for tests.
type memStore map[string]string

func (m memStore) Name() string { return "memory" }
func (m memStore) Get(k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (m memStore) Set(k, v string) error { m[k] = v; return nil }
func (m memStore) Delete(k string) error { delete(m, k); return nil }

func TestCredentialsRoundTrip(t *testing.T) {
	store := memStore{}
	want := Credentials{AppKey: "abc", ClientKey: "def"}
	if err := SaveCredentials(store, testBridgeID, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCredentials(store, testBridgeID)
	if err != nil || got != want {
		t.Errorf("got %+v, %v; want %+v", got, err, want)
	}
	_, err = LoadCredentials(store, "other")
	if !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("missing bridge: got %v, want ErrNotFound", err)
	}
}
