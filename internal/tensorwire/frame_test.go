package tensorwire

import (
	"bytes"
	"testing"
)

func TestFrameRoundTripAndAuthentication(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	wire := new(bytes.Buffer)
	want := Frame{SessionID: 8, Sequence: 9, Position: 10, Values: []float32{1.25, -2.5, 3.75}}
	if err := WriteFrame(wire, key, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(wire, key)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != want.SessionID || got.Sequence != want.Sequence || got.Position != want.Position || len(got.Values) != len(want.Values) {
		t.Fatalf("frame mismatch: %#v", got)
	}
	for i := range want.Values {
		if got.Values[i] != want.Values[i] {
			t.Fatalf("value %d mismatch", i)
		}
	}

	wire.Reset()
	if err := WriteFrame(wire, key, want); err != nil {
		t.Fatal(err)
	}
	data := wire.Bytes()
	data[headerSize+2] ^= 0xff
	if _, err := ReadFrame(bytes.NewReader(data), key); err == nil {
		t.Fatal("tampered tensor frame was accepted")
	}
}
