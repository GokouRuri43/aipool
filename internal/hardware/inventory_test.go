package hardware

import "testing"

func TestParseNvidiaSMI(t *testing.T) {
	input := []byte("0, NVIDIA Test GPU, GPU-test, 8192, 6144, 000.00, 9.0\n")
	devices, err := ParseNvidiaSMI(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].MemoryFreeMB != 6144 || devices[0].ComputeCapability != "9.0" {
		t.Fatalf("unexpected devices: %#v", devices)
	}
}

func TestParseNvidiaSMIRejectsMalformedRows(t *testing.T) {
	if _, err := ParseNvidiaSMI([]byte("not,a,valid,row\n")); err == nil {
		t.Fatal("expected parse error")
	}
}
