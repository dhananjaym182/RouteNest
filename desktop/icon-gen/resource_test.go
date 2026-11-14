package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

func TestCommittedWindowsResourceMatchesGenerator(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("..", "..", "resource_windows_amd64.syso"))
	if err != nil {
		t.Fatalf("read committed root resource: %v", err)
	}
	if generated := generatedResource(t); !bytes.Equal(committed, generated) {
		t.Fatal("root Windows resource is stale; regenerate with go run . -out ../../resource_windows_amd64.syso")
	}
	info, err := version.FromBytes(resourceData(t, committed, uint32(winres.RT_VERSION), 1, version.LangDefault))
	if err != nil {
		t.Fatalf("decode committed resource version: %v", err)
	}
	if info.FileVersion != [4]uint16{0, 1, 0, 1} || info.ProductVersion != [4]uint16{0, 1, 0, 1} || info.Table().GetMainTranslation()[version.ProductVersion] != "v0.1.0-beta.1" || !info.Flags.Prerelease {
		t.Fatalf("committed resource does not describe the first preview: %+v", info)
	}
}

func TestWindowsResourceIconSizes(t *testing.T) {
	object := generatedResource(t)
	group := resourceData(t, object, uint32(winres.RT_GROUP_ICON), 3, winres.LCIDNeutral)
	if len(group) != 6+4*14 {
		t.Fatalf("icon group size = %d, want %d", len(group), 6+4*14)
	}
	for i, size := range []byte{0, 48, 32, 16} {
		entry := group[6+i*14 : 6+(i+1)*14]
		if entry[0] != size || entry[1] != size || binary.LittleEndian.Uint16(entry[6:]) != 32 {
			t.Errorf("icon size/depth %d = %x", i, entry)
		}
		id := binary.LittleEndian.Uint16(entry[12:])
		image := resourceData(t, object, uint32(winres.RT_ICON), uint32(id), winres.LCIDNeutral)
		if uint32(len(image)) != binary.LittleEndian.Uint32(entry[8:]) || !bytes.HasPrefix(image, []byte("\x89PNG\r\n\x1a\n")) {
			t.Errorf("icon image %d is missing, has the wrong size or is not PNG", id)
		}
	}
}
