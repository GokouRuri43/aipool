package networking

import "testing"

func TestVirtualInterfaceClassification(t *testing.T) {
	for _, name := range []string{"vEthernet (WSL)", "DockerNAT", "VMware Network Adapter"} {
		if !isVirtualInterface(name) {
			t.Fatalf("expected %q to be virtual", name)
		}
	}
	if isVirtualInterface("Wi-Fi") {
		t.Fatal("Wi-Fi should not be classified as virtual")
	}
}
