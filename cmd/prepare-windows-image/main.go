// prepare-windows-image validates release EXEs with the legacy updater's PE
// parser and repairs UPX's dangling pointer to an empty COFF symbol table.
// It runs before packaging, hashing and signing; client checks stay unchanged.
package main

import (
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func prepareImage(data []byte) ([]byte, bool, error) {
	if len(data) < 64 || string(data[:2]) != "MZ" {
		return nil, false, errors.New("missing DOS executable header")
	}
	offset := uint64(binary.LittleEndian.Uint32(data[60:64]))
	if offset < 64 || offset+24 > uint64(len(data)) || string(data[offset:offset+4]) != "PE\x00\x00" {
		return nil, false, errors.New("invalid PE header offset or signature")
	}
	pointer := uint64(binary.LittleEndian.Uint32(data[offset+12 : offset+16]))
	symbols := binary.LittleEndian.Uint32(data[offset+16 : offset+20])
	repaired := false
	if symbols == 0 && pointer != 0 && pointer+4 > uint64(len(data)) {
		optionalSize := uint64(binary.LittleEndian.Uint16(data[offset+20 : offset+22]))
		count := uint64(binary.LittleEndian.Uint16(data[offset+6 : offset+8]))
		start := offset + 24 + optionalSize
		if count < 2 || count > 96 || start+count*40 > uint64(len(data)) || optionalSize < 152 {
			return nil, false, errors.New("invalid packed PE headers")
		}
		if binary.LittleEndian.Uint16(data[offset+24:offset+26]) != 0x20b ||
			string(bytes.TrimRight(data[start:start+8], "\x00")) != "UPX0" ||
			string(bytes.TrimRight(data[start+40:start+48], "\x00")) != "UPX1" {
			return nil, false, errors.New("dangling COFF pointer is not from a supported UPX PE32+ image")
		}
		// Never invalidate Authenticode by repairing an already signed image.
		security := data[offset+24+112+4*8 : offset+24+112+5*8]
		if !bytes.Equal(security, make([]byte, 8)) {
			return nil, false, errors.New("normalize the image before Authenticode signing")
		}
		data = bytes.Clone(data)
		binary.LittleEndian.PutUint32(data[offset+12:offset+16], 0)
		repaired = true
	}
	image, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("legacy Windows updater PE validation: %w", err)
	}
	defer image.Close()
	if image.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || image.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || image.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return nil, false, errors.New("expected an AMD64 Windows executable")
	}
	if _, ok := image.OptionalHeader.(*pe.OptionalHeader64); !ok {
		return nil, false, errors.New("expected a PE32+ optional header")
	}
	return data, repaired, nil
}

func run(input, output string) error {
	if input == "" || output == "" {
		return errors.New("both -in and -out are required; output must not exist")
	}
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	defer f.Close()
	const limit = 512 << 20
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if len(data) > limit {
		return errors.New("Windows image exceeds release size limit")
	}
	data, repaired, err := prepareImage(data)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	if _, err = out.Write(data); err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(output)
		return err
	}
	hash := sha256.Sum256(data)
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"file": output, "bytes": len(data), "sha256": hex.EncodeToString(hash[:]), "emptyCOFFPointerRepaired": repaired, "legacyPEValidation": "passed"})
}

func main() {
	input := flag.String("in", "", "packed Windows EXE")
	output := flag.String("out", "", "new validated EXE path")
	flag.Parse()
	if err := run(*input, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
