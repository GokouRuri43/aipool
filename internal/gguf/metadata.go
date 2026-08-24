package gguf

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	maxMetadataEntries = 1 << 20
	maxStringBytes     = 64 << 20
)

type Metadata struct {
	Version         uint32
	TensorCount     uint64
	Architecture    string
	LayerCount      int
	EmbeddingLength int
	ContextLength   int
	FileSize        int64
	TensorBytes     int64
	NonLayerBytes   int64
	LayerBytes      []int64
	Values          map[string]any
}

func ReadFile(path string) (Metadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return Metadata{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Metadata{}, err
	}
	meta, err := Read(file)
	if err != nil {
		return Metadata{}, err
	}
	meta.FileSize = info.Size()
	return meta, nil
}

func Read(r io.Reader) (Metadata, error) {
	reader := bufio.NewReaderSize(r, 128<<10)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(reader, magic); err != nil {
		return Metadata{}, err
	}
	if string(magic) != "GGUF" {
		return Metadata{}, fmt.Errorf("not a GGUF file")
	}
	var version uint32
	if err := binary.Read(reader, binary.LittleEndian, &version); err != nil {
		return Metadata{}, err
	}
	if version < 2 || version > 3 {
		return Metadata{}, fmt.Errorf("unsupported GGUF version %d", version)
	}
	var tensorCount, metadataCount uint64
	if err := binary.Read(reader, binary.LittleEndian, &tensorCount); err != nil {
		return Metadata{}, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &metadataCount); err != nil {
		return Metadata{}, err
	}
	if metadataCount > maxMetadataEntries || tensorCount > maxMetadataEntries {
		return Metadata{}, fmt.Errorf("GGUF contains too many entries")
	}
	values := make(map[string]any, metadataCount)
	for range metadataCount {
		key, err := readString(reader)
		if err != nil {
			return Metadata{}, fmt.Errorf("read GGUF metadata key: %w", err)
		}
		var valueType uint32
		if err := binary.Read(reader, binary.LittleEndian, &valueType); err != nil {
			return Metadata{}, err
		}
		value, err := readValue(reader, valueType, 0)
		if err != nil {
			return Metadata{}, fmt.Errorf("read GGUF metadata %q: %w", key, err)
		}
		values[key] = value
	}
	meta := Metadata{Version: version, TensorCount: tensorCount, Values: values}
	meta.Architecture, _ = values["general.architecture"].(string)
	meta.LayerCount = intValue(values[meta.Architecture+".block_count"])
	meta.EmbeddingLength = intValue(values[meta.Architecture+".embedding_length"])
	meta.ContextLength = intValue(values[meta.Architecture+".context_length"])
	if meta.Architecture == "" || meta.LayerCount <= 0 || meta.EmbeddingLength <= 0 {
		return Metadata{}, fmt.Errorf("GGUF lacks required architecture, block_count or embedding_length metadata")
	}
	meta.LayerBytes = make([]int64, meta.LayerCount)
	for range tensorCount {
		name, dimensions, ggmlType, err := readTensorInfo(reader)
		if err != nil {
			return Metadata{}, err
		}
		bytes, err := tensorStorageBytes(dimensions, ggmlType)
		if err != nil {
			return Metadata{}, fmt.Errorf("tensor %q: %w", name, err)
		}
		meta.TensorBytes += bytes
		if layer, ok := tensorLayer(name, meta.LayerCount); ok {
			meta.LayerBytes[layer] += bytes
		} else {
			meta.NonLayerBytes += bytes
		}
	}
	return meta, nil
}

func (m Metadata) Keys() []string {
	keys := make([]string, 0, len(m.Values))
	for key := range m.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func readTensorInfo(r io.Reader) (string, []uint64, uint32, error) {
	name, err := readString(r)
	if err != nil {
		return "", nil, 0, err
	}
	var nDimensions uint32
	if err := binary.Read(r, binary.LittleEndian, &nDimensions); err != nil {
		return "", nil, 0, err
	}
	if nDimensions == 0 || nDimensions > 4 {
		return "", nil, 0, fmt.Errorf("tensor %q has invalid dimension count", name)
	}
	dimensions := make([]uint64, nDimensions)
	for i := range dimensions {
		if err := binary.Read(r, binary.LittleEndian, &dimensions[i]); err != nil {
			return "", nil, 0, err
		}
	}
	var ggmlType uint32
	if err := binary.Read(r, binary.LittleEndian, &ggmlType); err != nil {
		return "", nil, 0, err
	}
	var offset uint64
	if err := binary.Read(r, binary.LittleEndian, &offset); err != nil {
		return "", nil, 0, err
	}
	return name, dimensions, ggmlType, nil
}

func tensorStorageBytes(dimensions []uint64, ggmlType uint32) (int64, error) {
	elements := uint64(1)
	for _, dimension := range dimensions {
		if dimension == 0 || elements > ^uint64(0)/dimension {
			return 0, fmt.Errorf("invalid tensor dimensions")
		}
		elements *= dimension
	}
	// GGML type traits expressed as block elements and bytes per block.
	traits := map[uint32][2]uint64{
		0: {1, 4}, 1: {1, 2}, 2: {32, 18}, 3: {32, 20}, 6: {32, 22}, 7: {32, 24}, 8: {32, 34}, 9: {32, 36},
		10: {256, 84}, 11: {256, 110}, 12: {256, 144}, 13: {256, 176}, 14: {256, 210}, 15: {256, 292},
		16: {256, 66}, 17: {256, 74}, 18: {256, 98}, 19: {256, 130}, 20: {256, 162}, 21: {256, 194}, 22: {256, 226}, 23: {256, 258},
		24: {1, 1}, 25: {1, 2}, 26: {1, 4}, 27: {1, 8}, 28: {1, 8}, 30: {1, 2},
	}
	trait, ok := traits[ggmlType]
	if !ok {
		return 0, fmt.Errorf("unsupported GGML tensor type %d", ggmlType)
	}
	blocks := (elements + trait[0] - 1) / trait[0]
	if blocks > uint64(^uint64(0)>>1)/trait[1] {
		return 0, fmt.Errorf("tensor is too large")
	}
	return int64(blocks * trait[1]), nil
}

func tensorLayer(name string, count int) (int, bool) {
	for _, marker := range []string{"blk.", "block."} {
		index := strings.Index(name, marker)
		if index < 0 {
			continue
		}
		rest := name[index+len(marker):]
		end := strings.IndexByte(rest, '.')
		if end < 0 {
			continue
		}
		layer, err := strconv.Atoi(rest[:end])
		if err == nil && layer >= 0 && layer < count {
			return layer, true
		}
	}
	return 0, false
}

func readString(r io.Reader) (string, error) {
	var length uint64
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return "", err
	}
	if length > maxStringBytes {
		return "", fmt.Errorf("GGUF string is too large")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return "", err
	}
	return string(data), nil
}

func readValue(r io.Reader, valueType uint32, depth int) (any, error) {
	if depth > 4 {
		return nil, fmt.Errorf("GGUF array nesting is too deep")
	}
	switch valueType {
	case 0:
		var v uint8
		return v, binary.Read(r, binary.LittleEndian, &v)
	case 1:
		var v int8
		return v, binary.Read(r, binary.LittleEndian, &v)
	case 2:
		var v uint16
		return v, binary.Read(r, binary.LittleEndian, &v)
	case 3:
		var v int16
		return v, binary.Read(r, binary.LittleEndian, &v)
	case 4:
		var v uint32
		return v, binary.Read(r, binary.LittleEndian, &v)
	case 5:
		var v int32
		return v, binary.Read(r, binary.LittleEndian, &v)
	case 6:
		var v float32
		return v, binary.Read(r, binary.LittleEndian, &v)
	case 7:
		var v uint8
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v != 0, nil
	case 8:
		return readString(r)
	case 9:
		var elementType uint32
		var length uint64
		if err := binary.Read(r, binary.LittleEndian, &elementType); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
			return nil, err
		}
		if length > maxMetadataEntries {
			return nil, fmt.Errorf("GGUF array is too large")
		}
		values := make([]any, length)
		for i := range values {
			value, err := readValue(r, elementType, depth+1)
			if err != nil {
				return nil, err
			}
			values[i] = value
		}
		return values, nil
	case 10:
		var v uint64
		return v, binary.Read(r, binary.LittleEndian, &v)
	case 11:
		var v int64
		return v, binary.Read(r, binary.LittleEndian, &v)
	case 12:
		var v float64
		return v, binary.Read(r, binary.LittleEndian, &v)
	default:
		return nil, fmt.Errorf("unknown GGUF value type %d", valueType)
	}
}

func intValue(value any) int {
	switch v := value.(type) {
	case uint8:
		return int(v)
	case int8:
		return int(v)
	case uint16:
		return int(v)
	case int16:
		return int(v)
	case uint32:
		return int(v)
	case int32:
		return int(v)
	case uint64:
		return int(v)
	case int64:
		return int(v)
	default:
		return 0
	}
}
