package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"testing"
)

func packedFixture(t *testing.T) []byte {
	t.Helper()
	data := make([]byte, 512)
	copy(data, "MZ")
	binary.LittleEndian.PutUint32(data[60:64], 128)
	copy(data[128:], "PE\x00\x00")
	var headers bytes.Buffer
	err := binary.Write(&headers, binary.LittleEndian, pe.FileHeader{Machine: pe.IMAGE_FILE_MACHINE_AMD64, NumberOfSections: 2, PointerToSymbolTable: 4096, SizeOfOptionalHeader: 240, Characteristics: 0x22})
	if err != nil {
		t.Fatal(err)
	}
	err = binary.Write(&headers, binary.LittleEndian, pe.OptionalHeader64{Magic: 0x20b, NumberOfRvaAndSizes: 16})
	if err != nil {
		t.Fatal(err)
	}
	copy(data[132:], headers.Bytes())
	copy(data[392:], "UPX0")
	copy(data[432:], "UPX1")
	return data
}

func TestRepairEmptyPackedCOFFPointer(t *testing.T) {
	before := packedFixture(t)
	if _, err := pe.NewFile(bytes.NewReader(before)); err == nil {
		t.Fatal("fixture must reproduce the old updater rejection")
	}
	after, repaired, err := prepareImage(before)
	if err != nil || !repaired {
		t.Fatalf("repair: %v, %v", repaired, err)
	}
	if binary.LittleEndian.Uint32(before[140:144]) != 4096 {
		t.Fatal("input was modified")
	}
	want := bytes.Clone(before)
	clear(want[140:144])
	if !bytes.Equal(after, want) {
		t.Fatal("repair changed bytes outside the unused COFF pointer")
	}
	if _, err := pe.NewFile(bytes.NewReader(after)); err != nil {
		t.Fatal(err)
	}
	again, repaired, err := prepareImage(after)
	if err != nil || repaired || !bytes.Equal(after, again) {
		t.Fatal("valid images must remain byte-identical")
	}
}

func TestRejectUnrelatedOrSignedImages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]byte)
	}{
		{"real symbols", func(b []byte) { binary.LittleEndian.PutUint32(b[144:148], 1) }},
		{"not UPX", func(b []byte) { copy(b[392:], ".text") }},
		{"signed", func(b []byte) { binary.LittleEndian.PutUint32(b[296:300], 480) }},
		{"wrong architecture", func(b []byte) { binary.LittleEndian.PutUint16(b[132:134], pe.IMAGE_FILE_MACHINE_ARM64) }},
		{"DLL", func(b []byte) { binary.LittleEndian.PutUint16(b[150:152], 0x2022) }},
		{"overflow offset", func(b []byte) { binary.LittleEndian.PutUint32(b[60:64], 0xffffffff) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := packedFixture(t)
			tc.change(b)
			if _, _, err := prepareImage(b); err == nil {
				t.Fatal("accepted unsupported image")
			}
		})
	}
	for _, size := range []int{0, 2, 63, 150, 471} {
		if _, _, err := prepareImage(packedFixture(t)[:size]); err == nil {
			t.Fatalf("accepted truncated headers: %d", size)
		}
	}
}
