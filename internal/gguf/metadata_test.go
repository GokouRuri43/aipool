package gguf

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestReadMinimalGGUFMetadataAndLayerSizes(t *testing.T) {
	buffer := new(bytes.Buffer)
	buffer.WriteString("GGUF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(3))
	_ = binary.Write(buffer, binary.LittleEndian, uint64(3))
	_ = binary.Write(buffer, binary.LittleEndian, uint64(4))
	writeMetadataString(buffer, "general.architecture", "test")
	writeMetadataUint32(buffer, "test.block_count", 2)
	writeMetadataUint32(buffer, "test.embedding_length", 8)
	writeMetadataUint32(buffer, "test.context_length", 128)
	writeTensor(buffer, "blk.0.weight", []uint64{32}, 2)
	writeTensor(buffer, "blk.1.weight", []uint64{32}, 0)
	writeTensor(buffer, "token_embd.weight", []uint64{32}, 1)
	metadata, err := Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Architecture != "test" || metadata.LayerCount != 2 || metadata.EmbeddingLength != 8 || metadata.ContextLength != 128 {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	if metadata.LayerBytes[0] != 18 || metadata.LayerBytes[1] != 128 || metadata.TensorBytes != 210 || metadata.NonLayerBytes != 64 {
		t.Fatalf("unexpected tensor sizes: %#v", metadata.LayerBytes)
	}
}

func TestReadRejectsInvalidMagic(t *testing.T) {
	if _, err := Read(bytes.NewReader([]byte("nope"))); err == nil {
		t.Fatal("invalid GGUF magic was accepted")
	}
}

func writeString(buffer *bytes.Buffer, value string) {
	_ = binary.Write(buffer, binary.LittleEndian, uint64(len(value)))
	buffer.WriteString(value)
}
func writeMetadataString(buffer *bytes.Buffer, key, value string) {
	writeString(buffer, key)
	_ = binary.Write(buffer, binary.LittleEndian, uint32(8))
	writeString(buffer, value)
}
func writeMetadataUint32(buffer *bytes.Buffer, key string, value uint32) {
	writeString(buffer, key)
	_ = binary.Write(buffer, binary.LittleEndian, uint32(4))
	_ = binary.Write(buffer, binary.LittleEndian, value)
}
func writeTensor(buffer *bytes.Buffer, name string, dimensions []uint64, ggmlType uint32) {
	writeString(buffer, name)
	_ = binary.Write(buffer, binary.LittleEndian, uint32(len(dimensions)))
	for _, dimension := range dimensions {
		_ = binary.Write(buffer, binary.LittleEndian, dimension)
	}
	_ = binary.Write(buffer, binary.LittleEndian, ggmlType)
	_ = binary.Write(buffer, binary.LittleEndian, uint64(0))
}
