package appresolver

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ksu-proxy/internal/config"
)

func TestLabelFromAPKUsesLiteralManifestApplicationLabel(t *testing.T) {
	path := writeTestAPK(t, buildManifestWithLabel(testLabelAttr{
		rawIndex: 3,
		dataType: typeString,
		data:     3,
	}), nil)

	label, err := labelFromAPK(path, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if label != "Solar Smash" {
		t.Fatalf("label = %q", label)
	}
}

func TestLabelFromAPKResolvesLocalizedResourceLabel(t *testing.T) {
	const resID = 0x7f010000
	path := writeTestAPK(t, buildManifestWithLabel(testLabelAttr{
		rawIndex: noIndex,
		dataType: typeReference,
		data:     resID,
	}), buildResourceTable(resID, "Default App", "中文应用"))

	label, err := labelFromAPK(path, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if label != "中文应用" {
		t.Fatalf("label = %q", label)
	}
}

func TestListInstalledFillsAPKLabels(t *testing.T) {
	path := writeTestAPK(t, buildManifestWithLabel(testLabelAttr{
		rawIndex: 3,
		dataType: typeString,
		data:     3,
	}), nil)
	r := Resolver{
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			_ = ctx
			if name == "getprop" {
				return []byte("zh-CN\n"), nil
			}
			if name == "cmd" && len(args) >= 2 && args[0] == "user" && args[1] == "list" {
				return []byte("Users:\n\tUserInfo{0:Owner:13} running\n"), nil
			}
			if name == "cmd" && len(args) >= 7 && args[0] == "package" && args[3] == "--user" && args[4] == "0" {
				return []byte("package:" + path + "=com.example.solar uid:10367\n"), nil
			}
			return nil, os.ErrNotExist
		},
	}
	apps, err := r.ListInstalled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 {
		t.Fatalf("len(apps) = %d", len(apps))
	}
	if apps[0].Label != "Solar Smash" {
		t.Fatalf("Label = %q", apps[0].Label)
	}
	if got := DisplayName(apps[0].PackageName, apps[0].Label); got != "Solar Smash" {
		t.Fatalf("DisplayName = %q", got)
	}
}

func TestResolveWhitelistDoesNotReadAPKLabels(t *testing.T) {
	path := writeTestAPK(t, buildManifestWithLabel(testLabelAttr{
		rawIndex: 3,
		dataType: typeString,
		data:     3,
	}), nil)
	r := Resolver{
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			_ = ctx
			if name == "getprop" {
				t.Fatalf("ResolveWhitelist should not query locale or parse APK labels")
			}
			if name == "cmd" && len(args) >= 2 && args[0] == "user" && args[1] == "list" {
				return []byte("Users:\n\tUserInfo{0:Owner:13} running\n"), nil
			}
			if name == "cmd" && len(args) >= 7 && args[0] == "package" && args[3] == "--user" && args[4] == "0" {
				return []byte("package:" + path + "=com.example.solar uid:10367\n"), nil
			}
			return nil, os.ErrNotExist
		},
	}
	apps, err := r.ResolveWhitelist(context.Background(), []config.PackageRule{
		{PackageName: "com.example.solar", Scope: "all_instances"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 {
		t.Fatalf("len(apps) = %d", len(apps))
	}
	if apps[0].Label != "" {
		t.Fatalf("Label = %q, want empty for firewall reconcile path", apps[0].Label)
	}
}

type testLabelAttr struct {
	rawIndex uint32
	dataType byte
	data     uint32
}

func writeTestAPK(t *testing.T, manifest []byte, resources []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.apk")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	addZipEntry(t, zw, "AndroidManifest.xml", manifest)
	if len(resources) > 0 {
		addZipEntry(t, zw, "resources.arsc", resources)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func addZipEntry(t *testing.T, zw *zip.Writer, name string, data []byte) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
}

func buildManifestWithLabel(attr testLabelAttr) []byte {
	stringPool := buildTestStringPool([]string{"manifest", "application", "label", "Solar Smash"})
	manifestElement := buildStartElement(0, nil)
	applicationElement := buildStartElement(1, []testLabelAttr{attr})
	var body bytes.Buffer
	body.Write(stringPool)
	body.Write(manifestElement)
	body.Write(applicationElement)
	return buildChunk(resXMLType, 8, body.Bytes())
}

func buildStartElement(nameIndex uint32, attrs []testLabelAttr) []byte {
	var body bytes.Buffer
	writeU32(&body, 0)
	writeU32(&body, noIndex)
	writeU32(&body, noIndex)
	writeU32(&body, nameIndex)
	writeU16(&body, 20)
	writeU16(&body, 20)
	writeU16(&body, uint16(len(attrs)))
	writeU16(&body, 0)
	writeU16(&body, 0)
	writeU16(&body, 0)
	for _, attr := range attrs {
		writeU32(&body, noIndex)
		writeU32(&body, 2)
		writeU32(&body, attr.rawIndex)
		writeU16(&body, 8)
		body.WriteByte(0)
		body.WriteByte(attr.dataType)
		writeU32(&body, attr.data)
	}
	return buildChunk(resXMLStartElement, 36, body.Bytes())
}

func buildResourceTable(resID uint32, defaultLabel string, zhLabel string) []byte {
	valueStrings := buildTestStringPool([]string{defaultLabel, zhLabel})
	typeStrings := buildTestStringPool([]string{"string"})
	keyStrings := buildTestStringPool([]string{"app_name"})
	typeID := byte((resID >> 16) & 0xff)
	entryID := int(resID & 0xffff)
	defaultType := buildTypeChunk(typeID, entryID, 0, "", "")
	zhType := buildTypeChunk(typeID, entryID, 1, "zh", "CN")

	headerSize := 288
	var pkg bytes.Buffer
	pkg.Write(make([]byte, headerSize-8))
	pkgBytes := pkg.Bytes()
	binary.LittleEndian.PutUint32(pkgBytes[0:], uint32(resID>>24))
	typeStringsOff := headerSize
	keyStringsOff := typeStringsOff + len(typeStrings)
	binary.LittleEndian.PutUint32(pkgBytes[260:], uint32(typeStringsOff))
	binary.LittleEndian.PutUint32(pkgBytes[268:], uint32(keyStringsOff))

	var pkgBody bytes.Buffer
	pkgBody.Write(pkgBytes)
	pkgBody.Write(typeStrings)
	pkgBody.Write(keyStrings)
	pkgBody.Write(defaultType)
	pkgBody.Write(zhType)
	packageChunk := buildChunk(resTablePackageType, uint16(headerSize), pkgBody.Bytes())

	var tableBody bytes.Buffer
	writeU32(&tableBody, 1)
	tableBody.Write(valueStrings)
	tableBody.Write(packageChunk)
	return buildChunk(resTableType, 12, tableBody.Bytes())
}

func buildTypeChunk(typeID byte, entryID int, valueStringIndex uint32, lang string, country string) []byte {
	configSize := 64
	headerSize := 20 + configSize
	entryCount := entryID + 1
	entriesStart := headerSize + entryCount*4

	var body bytes.Buffer
	body.WriteByte(typeID)
	body.WriteByte(0)
	writeU16(&body, 0)
	writeU32(&body, uint32(entryCount))
	writeU32(&body, uint32(entriesStart))
	config := make([]byte, configSize)
	binary.LittleEndian.PutUint32(config[0:], uint32(configSize))
	if len(lang) == 2 {
		copy(config[8:10], strings.ToLower(lang))
	}
	if len(country) == 2 {
		copy(config[10:12], strings.ToUpper(country))
	}
	body.Write(config)
	for i := 0; i < entryCount; i++ {
		if i == entryID {
			writeU32(&body, 0)
		} else {
			writeU32(&body, noIndex)
		}
	}
	writeU16(&body, 8)
	writeU16(&body, 0)
	writeU32(&body, 0)
	writeU16(&body, 8)
	body.WriteByte(0)
	body.WriteByte(typeString)
	writeU32(&body, valueStringIndex)
	return buildChunk(resTableTypeType, uint16(headerSize), body.Bytes())
}

func buildTestStringPool(values []string) []byte {
	headerSize := 28
	var data bytes.Buffer
	offsets := make([]uint32, 0, len(values))
	for _, value := range values {
		offsets = append(offsets, uint32(data.Len()))
		raw := []byte(value)
		writeLength8(&data, len([]rune(value)))
		writeLength8(&data, len(raw))
		data.Write(raw)
		data.WriteByte(0)
	}
	stringsStart := headerSize + len(values)*4
	var body bytes.Buffer
	writeU32(&body, uint32(len(values)))
	writeU32(&body, 0)
	writeU32(&body, stringPoolUTF8Flag)
	writeU32(&body, uint32(stringsStart))
	writeU32(&body, 0)
	for _, offset := range offsets {
		writeU32(&body, offset)
	}
	body.Write(data.Bytes())
	return buildChunk(resStringPoolType, uint16(headerSize), body.Bytes())
}

func buildChunk(chunkType uint16, headerSize uint16, body []byte) []byte {
	size := 8 + len(body)
	for size%4 != 0 {
		body = append(body, 0)
		size++
	}
	var out bytes.Buffer
	writeU16(&out, chunkType)
	writeU16(&out, headerSize)
	writeU32(&out, uint32(size))
	out.Write(body)
	return out.Bytes()
}

func writeLength8(buf *bytes.Buffer, value int) {
	if value < 0x80 {
		buf.WriteByte(byte(value))
		return
	}
	buf.WriteByte(byte((value >> 8) | 0x80))
	buf.WriteByte(byte(value))
}

func writeU16(buf *bytes.Buffer, value uint16) {
	_ = binary.Write(buf, binary.LittleEndian, value)
}

func writeU32(buf *bytes.Buffer, value uint32) {
	_ = binary.Write(buf, binary.LittleEndian, value)
}
