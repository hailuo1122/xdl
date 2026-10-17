package bittorrent

import (
	"testing"

	"github.com/anacrolix/torrent"
)

// TestClientOpen checks that anacrolix builds and starts on this machine
// without DHT, TCP, or uTP. It does not contact the network.
func TestClientOpen(t *testing.T) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = t.TempDir()
	cfg.NoUpload = true
	cfg.NoDHT = true
	cfg.DisablePEX = true
	cfg.DisableTCP = true
	cfg.DisableUTP = true
	cfg.NoDefaultPortForwarding = true
	cfg.DisableIPv6 = true
	cfg.ListenPort = 0

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cl.Close()
}
