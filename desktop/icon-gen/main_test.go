package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

func generatedResource(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := writeResource(&out); err != nil {
		t.Fatalf("generate resource: %v", err)
	}
	return out.Bytes()
}

func resourceData(t *testing.T, object []byte, resourceType, resourceID uint32, language uint16) []byte {
	t.Helper()
	coff, err := pe.NewFile(bytes.NewReader(object))
	if err != nil {
		t.Fatalf("read COFF object: %v", err)
	}
	defer coff.Close()
	if coff.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || coff.NumberOfSections != 1 || coff.TimeDateStamp != 0 {
		t.Fatalf("unexpected resource header: %+v", coff.FileHeader)
	}
	section := coff.Section(".rsrc")
	if section == nil {
		t.Fatal("missing resource section")
	}
	data, err := section.Data()
	if err != nil {
		t.Fatalf("read resource section: %v", err)
	}
	offset := uint32(0)
	for level, id := range []uint32{resourceType, resourceID, uint32(language)} {
		if uint64(offset)+16 > uint64(len(data)) {
			t.Fatalf("resource directory offset %d is outside the section", offset)
		}
		count := uint32(binary.LittleEndian.Uint16(data[offset+12:])) + uint32(binary.LittleEndian.Uint16(data[offset+14:]))
		if uint64(offset)+16+uint64(count)*8 > uint64(len(data)) {
			t.Fatal("resource directory entries are outside the section")
		}
		found := false
		for i := uint32(0); i < count; i++ {
			entry := offset + 16 + i*8
			if binary.LittleEndian.Uint32(data[entry:]) != id {
				continue
			}
			target := binary.LittleEndian.Uint32(data[entry+4:])
			if (target&0x80000000 != 0) != (level < 2) {
				t.Fatalf("invalid resource directory at level %d", level)
			}
			offset, found = target&0x7fffffff, true
			break
		}
		if !found {
			t.Fatalf("missing resource type=%d ID=%d language=%d", resourceType, resourceID, language)
		}
	}
	if uint64(offset)+16 > uint64(len(data)) {
		t.Fatal("resource data entry is outside the section")
	}
	start, size := binary.LittleEndian.Uint32(data[offset:]), binary.LittleEndian.Uint32(data[offset+4:])
	if uint64(start)+uint64(size) > uint64(len(data)) {
		t.Fatal("resource payload is outside the section")
	}
	return data[start : start+size]
}

func TestWindowsPreviewVersionInfo(t *testing.T) {
	object := generatedResource(t)
	data := resourceData(t, object, uint32(winres.RT_VERSION), 1, version.LangDefault)
	info, err := version.FromBytes(data)
	if err != nil {
		t.Fatalf("decode version info: %v", err)
	}
	wantNumeric := [4]uint16{0, 1, 0, 1}
	if info.FileVersion != wantNumeric || info.ProductVersion != wantNumeric {
		t.Errorf("numeric file/product versions = %v/%v, want %v", info.FileVersion, info.ProductVersion, wantNumeric)
	}
	if !info.Flags.Prerelease || info.Flags.Debug || info.Flags.Patched || info.Flags.PrivateBuild || info.Flags.SpecialBuild || info.Type != version.App {
		t.Errorf("unexpected preview flags/type: %+v", info)
	}
	if !info.Timestamp.IsZero() {
		t.Errorf("resource timestamp = %v, want unset for reproducibility", info.Timestamp)
	}
	wantStrings := version.StringTable{
		version.CompanyName:      "dhananjaym182",
		version.ProductName:      "RouteNest",
		version.FileDescription:  "RouteNest Desktop",
		version.LegalCopyright:   "Copyright (c) 2026 dhananjaym182",
		version.OriginalFilename: "routenest-desktop.exe",
		version.FileVersion:      "0.1.0.1",
		version.ProductVersion:   "v0.1.0-beta.1",
	}
	if got := info.Table().GetMainTranslation(); !reflect.DeepEqual(got, wantStrings) {
		t.Errorf("version strings = %v, want %v", got, wantStrings)
	}
}

func TestWindowsResourceRetainsIconAndManifest(t *testing.T) {
	object := generatedResource(t)
	icon := resourceData(t, object, uint32(winres.RT_GROUP_ICON), 3, winres.LCIDNeutral)
	if len(icon) < 6 || binary.LittleEndian.Uint16(icon) != 0 || binary.LittleEndian.Uint16(icon[2:]) != 1 || binary.LittleEndian.Uint16(icon[4:]) != 4 {
		t.Fatalf("invalid four-size application icon: %x", icon)
	}
	manifest := string(resourceData(t, object, uint32(winres.RT_MANIFEST), 1, winres.LCIDDefault))
	for _, expected := range []string{"RouteNest Desktop", "asInvoker", "permonitorv2,system", "8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a"} {
		if !strings.Contains(manifest, expected) {
			t.Errorf("manifest is missing %q", expected)
		}
	}
}

func TestWindowsResourceReproducible(t *testing.T) {
	first := generatedResource(t)
	for i := 0; i < 3; i++ {
		if next := generatedResource(t); !bytes.Equal(first, next) {
			t.Fatalf("resource generation %d differs from the first", i+2)
		}
	}
}

type failingResourceWriter struct{ err error }

func (w failingResourceWriter) Write([]byte) (int, error) { return 0, w.err }

func TestWindowsResourceWriteError(t *testing.T) {
	if err := writeResource(failingResourceWriter{io.ErrClosedPipe}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writeResource error = %v, want %v", err, io.ErrClosedPipe)
	}
}
