package boot

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"

	"omloader/internal/config"
)

func fakeGPT(parts [][3]any) []byte { // {first, last, name}
	img := make([]byte, 40*512)
	copy(img[512:], "EFI PART")
	for i := 0; i < 16; i++ {
		img[512+56+i] = byte(i + 1)
	}
	binary.LittleEndian.PutUint64(img[512+72:], 2)
	binary.LittleEndian.PutUint32(img[512+80:], 8)
	binary.LittleEndian.PutUint32(img[512+84:], 128)
	for i, p := range parts {
		e := img[1024+i*128:]
		e[0] = 0xAF
		binary.LittleEndian.PutUint64(e[32:], uint64(p[0].(int)))
		binary.LittleEndian.PutUint64(e[40:], uint64(p[1].(int)))
		for j, u := range utf16.Encode([]rune(p[2].(string))) {
			binary.LittleEndian.PutUint16(e[56+j*2:], u)
		}
	}
	return img
}

func TestParseGPT(t *testing.T) {
	id, ps, err := ParsePartTable(bytes.NewReader(fakeGPT([][3]any{{2048, 4095, "EFI"}, {4096, 8191, "Daten 中"}})))
	if err != nil || len(ps) != 2 || !strings.HasPrefix(id, "gpt-0102") {
		t.Fatal(id, ps, err)
	}
	if ps[1].N != 2 || ps[1].Start != 4096 || ps[1].Size != 4096 || ps[1].PartLabel != "Daten 中" {
		t.Fatalf("%+v", ps[1])
	}
}

func TestParseMBRExtended(t *testing.T) {
	img := make([]byte, 20000*512)
	img[510], img[511] = 0x55, 0xAA
	binary.LittleEndian.PutUint32(img[440:], 0xdeadbeef)
	ent := func(b []byte, typ byte, st, sz uint32) {
		b[4] = typ
		binary.LittleEndian.PutUint32(b[8:], st)
		binary.LittleEndian.PutUint32(b[12:], sz)
	}
	ent(img[446:], 0x07, 2048, 1000)
	ent(img[462:], 0x0f, 5000, 10000)
	ebr := img[5000*512:]
	ebr[510], ebr[511] = 0x55, 0xAA
	ent(ebr[446:], 0x83, 63, 100)
	ent(ebr[462:], 0x05, 2000, 500)
	e2 := img[7000*512:]
	e2[510], e2[511] = 0x55, 0xAA
	ent(e2[446:], 0x83, 63, 200)
	id, ps, err := ParsePartTable(bytes.NewReader(img))
	if err != nil || id != "mbr-deadbeef" || len(ps) != 3 {
		t.Fatal(id, ps, err)
	}
	if ps[0].N != 1 || ps[0].Start != 2048 || ps[1].N != 5 || ps[1].Start != 5063 || ps[2].N != 6 || ps[2].Start != 7063 || ps[2].Size != 200 {
		t.Fatalf("%+v", ps)
	}
}

func TestSanitizeAndNames(t *testing.T) {
	for in, want := range map[string]string{
		"Data":                  "Data",
		"My Data/Backup":        "My_Data_Backup",
		"数据":                    "",
		"..hidden":              "hidden",
		"a  b":                  "a_b",
		strings.Repeat("x", 40): strings.Repeat("x", 32),
		"C:\\":                  "C_",
	} {
		if got := SanitizeName(in); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
	ps := []HDPart{{RawPart: RawPart{N: 1}, Label: "VTOYEFI"}, {RawPart: RawPart{N: 2, PartLabel: "Windows Data"}},
		{RawPart: RawPart{N: 3}, Label: "数据"}, {RawPart: RawPart{N: 4}, Label: "vtoyefi"}, {RawPart: RawPart{N: 5}, Label: strings.Repeat("y", 32)}, {RawPart: RawPart{N: 6}, Label: strings.Repeat("y", 32)}}
	AssignNames(ps)
	got := []string{ps[0].Name, ps[1].Name, ps[2].Name, ps[3].Name, ps[4].Name, ps[5].Name}
	want := []string{"VTOYEFI", "Windows_Data", "p3", "vtoyefi_p4", strings.Repeat("y", 32), strings.Repeat("y", 29) + "_p6"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %q want %q", i, got[i], want[i])
		}
	}
}

func TestFstabAndSkip(t *testing.T) {
	cfg := config.HostDisks{Mode: "ro", MountRoot: "/mnt/disks"}
	d := []HDDisk{{Name: "nvme0n1", Parts: []HDPart{
		{RawPart: RawPart{N: 4}, FSType: "ntfs", Name: "VHDX", RO: true},
		{RawPart: RawPart{N: 5}, FSType: "BitLocker", Name: "bl"}, {RawPart: RawPart{N: 6}, FSType: "crypto_LUKS", Name: "l"},
		{RawPart: RawPart{N: 7}, FSType: "swap", Name: "s"}, {RawPart: RawPart{N: 8}, FSType: "LVM2_member", Name: "v"},
		{RawPart: RawPart{N: 9}, Name: "unk"}, {RawPart: RawPart{N: 2}, FSType: "ext4", Name: "root", RO: true}}}}
	got := FstabLines(cfg, d)
	if len(got) != 2 || got[0] != "/dev/mapper/oml-nvme0n1p4  /mnt/disks/nvme0n1/VHDX  auto  noauto,x-systemd.automount,nofail,x-systemd.idle-timeout=60,ro  0 0" {
		t.Fatalf("%q", got)
	}
	f := false
	cfg.Automount = &f
	if l := FstabLines(cfg, d); strings.Contains(l[0], "automount") {
		t.Fatal(l[0])
	}
}

func TestRolesAndRO(t *testing.T) {
	if !PartRO(config.HostDisks{Mode: "ro"}, "") || PartRO(config.HostDisks{Mode: "rw"}, "") ||
		!PartRO(config.HostDisks{Mode: "rw"}, "data") || PartRO(config.HostDisks{Mode: "rw", ForceRWData: true}, "store") {
		t.Fatal("PartRO")
	}
	mi := "36 1 8:4 / /run/medium-host rw - ntfs3 /dev/vda4 rw\n"
	if DataMountSource(mi, "/run/medium-host") != "/dev/vda4" || DataMountSource(mi, "/x") != "" {
		t.Fatal("DataMountSource")
	}
}

func TestPlanTablesRebaseBlock(t *testing.T) {
	pl := HostDisksPlan([]HDDisk{{Name: "vda", ID: "x", Sectors: 1000, Parts: []HDPart{{RawPart: RawPart{N: 4, Start: 100, Size: 50}, RO: true, Name: "A", FSType: "ntfs"}}}})
	if DiskTable(1000, "/dev/vda") != "0 1000 linear /dev/vda 0\n" || PartTable(pl[0].Parts[0], "/dev/mapper/oml-vda") != "0 50 linear /dev/mapper/oml-vda 100\n" {
		t.Fatal("tables")
	}
	ex := RebaseExtents([]Extent{{Logical: 0, Physical: 4096, Length: 512}}, 100)
	if ex[0].Physical != 4096+51200 {
		t.Fatal(ex)
	}
	fs := ReplaceFstabBlock("/dev/x / ext4 defaults 0 1\n", []string{"L1"})
	fs2 := ReplaceFstabBlock(fs, []string{"L2"})
	if strings.Count(fs2, HostDisksBegin) != 1 || strings.Contains(fs2, "L1") || !strings.Contains(fs2, "L2") || ReplaceFstabBlock(fs2, nil) != "/dev/x / ext4 defaults 0 1\n" {
		t.Fatalf("%q", fs2)
	}
	cfg := config.HostDisks{Mode: "ro"}
	if rd := HostDisksReadme(cfg, DisksFromPlan(pl)); !strings.Contains(rd, "omloader/store.img") || !strings.Contains(rd, "/mnt/disks/vda/A") {
		t.Fatal(rd)
	}
}
