package appresolver

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	resStringPoolType    = 0x0001
	resTableType         = 0x0002
	resXMLType           = 0x0003
	resXMLResourceMap    = 0x0180
	resXMLStartElement   = 0x0102
	resTablePackageType  = 0x0200
	resTableTypeType     = 0x0201
	stringPoolUTF8Flag   = 0x00000100
	noIndex              = 0xffffffff
	typeReference        = 0x01
	typeString           = 0x03
	androidAttrLabel     = 0x01010001
	resourceEntryComplex = 0x0001
)

var apkLabelCache sync.Map

type apkLabelRef struct {
	literal string
	resID   uint32
}

func (r Resolver) fillLabels(ctx context.Context, apps []AppInstance) []AppInstance {
	locale := r.systemLocale(ctx)
	cache := make(map[string]string)
	for i := range apps {
		if strings.TrimSpace(apps[i].Label) != "" {
			continue
		}
		source := cleanSourcePath(apps[i].SourceDir)
		if source == "" {
			continue
		}
		label, ok := cache[source]
		if !ok {
			cacheKey, statOK := apkLabelCacheKey(source, locale)
			if statOK {
				if cached, loaded := apkLabelCache.Load(cacheKey); loaded {
					label = cached.(string)
					cache[source] = label
					if label != "" {
						apps[i].Label = label
					}
					continue
				}
			}
			var err error
			label, err = labelFromAPK(source, locale)
			if err != nil {
				label = ""
			}
			if statOK {
				apkLabelCache.Store(cacheKey, label)
			}
			cache[source] = label
		}
		if label != "" {
			apps[i].Label = label
		}
	}
	return apps
}

func apkLabelCacheKey(path string, locale string) (string, bool) {
	stat, err := os.Stat(path)
	if err != nil || stat.IsDir() {
		return "", false
	}
	return strings.Join([]string{
		path,
		stat.ModTime().UTC().Format("20060102150405.000000000"),
		fmt.Sprint(stat.Size()),
		locale,
	}, "\x00"), true
}

func (r Resolver) systemLocale(ctx context.Context) string {
	if r.Run == nil {
		r.Run = DefaultRunner
	}
	for _, prop := range []string{"persist.sys.locale", "ro.product.locale"} {
		raw, err := r.Run(ctx, "getprop", prop)
		if err != nil {
			continue
		}
		if locale := strings.TrimSpace(string(raw)); locale != "" {
			return locale
		}
	}
	return ""
}

func cleanSourcePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "package:")
	if path == "" || strings.Contains(path, "\n") {
		return ""
	}
	return path
}

func labelFromAPK(path string, preferredLocale string) (string, error) {
	if stat, err := os.Stat(path); err != nil || stat.IsDir() {
		if err == nil {
			err = fmt.Errorf("apk path is a directory")
		}
		return "", err
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	var manifest []byte
	var resources []byte
	for _, file := range reader.File {
		switch file.Name {
		case "AndroidManifest.xml":
			manifest, err = readZipFile(file, 2*1024*1024)
			if err != nil {
				return "", err
			}
		case "resources.arsc":
			resources, err = readZipFile(file, 32*1024*1024)
			if err != nil {
				return "", err
			}
		}
	}
	if len(manifest) == 0 {
		return "", fmt.Errorf("AndroidManifest.xml missing")
	}

	ref, err := parseManifestApplicationLabel(manifest)
	if err != nil {
		return "", err
	}
	if ref.literal != "" {
		return ref.literal, nil
	}
	if ref.resID != 0 && len(resources) > 0 {
		if label, ok := resolveResourceString(resources, ref.resID, preferredLocale); ok {
			return label, nil
		}
	}
	return "", fmt.Errorf("application label not found")
}

func readZipFile(file *zip.File, maxBytes int64) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxBytes))
}

func parseManifestApplicationLabel(data []byte) (apkLabelRef, error) {
	chunkType, headerSize, chunkSize, ok := readChunkHeader(data, 0)
	if !ok || chunkType != resXMLType || int(chunkSize) > len(data) {
		return apkLabelRef{}, fmt.Errorf("invalid binary manifest")
	}
	strings := []string{}
	resourceIDs := map[uint32]uint32{}
	for off := int(headerSize); off+8 <= int(chunkSize); {
		childType, childHeader, childSize, ok := readChunkHeader(data, off)
		if !ok || childSize == 0 || off+int(childSize) > len(data) {
			break
		}
		switch childType {
		case resStringPoolType:
			parsed, err := parseStringPool(data[off : off+int(childSize)])
			if err == nil {
				strings = parsed
			}
		case resXMLResourceMap:
			for pos := off + int(childHeader); pos+4 <= off+int(childSize); pos += 4 {
				index := uint32((pos - off - int(childHeader)) / 4)
				resourceIDs[index] = binary.LittleEndian.Uint32(data[pos:])
			}
		case resXMLStartElement:
			ref, found := parseStartElementForApplicationLabel(data[off:off+int(childSize)], strings, resourceIDs)
			if found {
				return ref, nil
			}
		}
		off += int(childSize)
	}
	return apkLabelRef{}, fmt.Errorf("application label not found")
}

func parseStartElementForApplicationLabel(chunk []byte, stringPool []string, resourceIDs map[uint32]uint32) (apkLabelRef, bool) {
	_, headerSize, chunkSize, ok := readChunkHeader(chunk, 0)
	if !ok || int(chunkSize) > len(chunk) || len(chunk) < int(headerSize) || len(chunk) < 36 {
		return apkLabelRef{}, false
	}
	elementNameIndex := binary.LittleEndian.Uint32(chunk[20:])
	if stringAt(stringPool, elementNameIndex) != "application" {
		return apkLabelRef{}, false
	}
	attrStart := int(binary.LittleEndian.Uint16(chunk[24:]))
	attrSize := int(binary.LittleEndian.Uint16(chunk[26:]))
	attrCount := int(binary.LittleEndian.Uint16(chunk[28:]))
	if attrSize <= 0 {
		return apkLabelRef{}, false
	}
	attrsOff := 16 + attrStart
	if attrsOff < int(headerSize) {
		attrsOff = int(headerSize)
	}
	for i := 0; i < attrCount; i++ {
		off := attrsOff + i*attrSize
		if off+20 > len(chunk) {
			break
		}
		nameIndex := binary.LittleEndian.Uint32(chunk[off+4:])
		name := stringAt(stringPool, nameIndex)
		if name != "label" && resourceIDs[nameIndex] != androidAttrLabel {
			continue
		}
		rawIndex := binary.LittleEndian.Uint32(chunk[off+8:])
		dataType := chunk[off+15]
		valueData := binary.LittleEndian.Uint32(chunk[off+16:])
		if rawIndex != noIndex {
			raw := strings.TrimSpace(stringAt(stringPool, rawIndex))
			if raw != "" && !strings.HasPrefix(raw, "@") {
				return apkLabelRef{literal: raw}, true
			}
		}
		switch dataType {
		case typeString:
			label := strings.TrimSpace(stringAt(stringPool, valueData))
			if label != "" {
				return apkLabelRef{literal: label}, true
			}
		case typeReference:
			if valueData != 0 {
				return apkLabelRef{resID: valueData}, true
			}
		}
	}
	return apkLabelRef{}, false
}

func resolveResourceString(data []byte, resID uint32, preferredLocale string) (string, bool) {
	chunkType, headerSize, chunkSize, ok := readChunkHeader(data, 0)
	if !ok || chunkType != resTableType || int(chunkSize) > len(data) {
		return "", false
	}
	var valueStrings []string
	packageID := byte(resID >> 24)
	for off := int(headerSize); off+8 <= int(chunkSize); {
		childType, _, childSize, ok := readChunkHeader(data, off)
		if !ok || childSize == 0 || off+int(childSize) > len(data) {
			break
		}
		switch childType {
		case resStringPoolType:
			if len(valueStrings) == 0 {
				if parsed, err := parseStringPool(data[off : off+int(childSize)]); err == nil {
					valueStrings = parsed
				}
			}
		case resTablePackageType:
			if label, ok := resolveResourceStringInPackage(data[off:off+int(childSize)], packageID, resID, valueStrings, preferredLocale); ok {
				return label, true
			}
		}
		off += int(childSize)
	}
	return "", false
}

func resolveResourceStringInPackage(pkg []byte, packageID byte, resID uint32, valueStrings []string, preferredLocale string) (string, bool) {
	_, headerSize, chunkSize, ok := readChunkHeader(pkg, 0)
	if !ok || int(chunkSize) > len(pkg) || len(pkg) < int(headerSize) || len(valueStrings) == 0 {
		return "", false
	}
	if len(pkg) < 12 || byte(binary.LittleEndian.Uint32(pkg[8:])) != packageID {
		return "", false
	}
	typeID := byte((resID >> 16) & 0xff)
	entryID := int(resID & 0xffff)
	best := resourceMatch{score: -1}
	for off := int(headerSize); off+8 <= int(chunkSize); {
		childType, childHeader, childSize, ok := readChunkHeader(pkg, off)
		if !ok || childSize == 0 || off+int(childSize) > len(pkg) {
			break
		}
		if childType == resTableTypeType && off+20 <= len(pkg) && pkg[off+8] == typeID {
			if label, score, ok := resolveResourceStringInType(pkg[off:off+int(childSize)], int(childHeader), entryID, valueStrings, preferredLocale); ok && score > best.score {
				best = resourceMatch{label: label, score: score}
			}
		}
		off += int(childSize)
	}
	return best.label, best.score >= 0
}

type resourceMatch struct {
	label string
	score int
}

func resolveResourceStringInType(chunk []byte, headerSize int, entryID int, valueStrings []string, preferredLocale string) (string, int, bool) {
	if len(chunk) < headerSize || len(chunk) < 20 {
		return "", 0, false
	}
	entryCount := int(binary.LittleEndian.Uint32(chunk[12:]))
	entriesStart := int(binary.LittleEndian.Uint32(chunk[16:]))
	if entryID < 0 || entryID >= entryCount {
		return "", 0, false
	}
	offsetTable := headerSize + entryID*4
	if offsetTable+4 > len(chunk) {
		return "", 0, false
	}
	entryOffset := binary.LittleEndian.Uint32(chunk[offsetTable:])
	if entryOffset == noIndex {
		return "", 0, false
	}
	entryStart := entriesStart + int(entryOffset)
	if entryStart+16 > len(chunk) {
		return "", 0, false
	}
	entrySize := int(binary.LittleEndian.Uint16(chunk[entryStart:]))
	flags := binary.LittleEndian.Uint16(chunk[entryStart+2:])
	if flags&resourceEntryComplex != 0 || entrySize <= 0 || entryStart+entrySize+8 > len(chunk) {
		return "", 0, false
	}
	valueOff := entryStart + entrySize
	dataType := chunk[valueOff+3]
	valueData := binary.LittleEndian.Uint32(chunk[valueOff+4:])
	if dataType != typeString {
		return "", 0, false
	}
	label := strings.TrimSpace(stringAt(valueStrings, valueData))
	if label == "" {
		return "", 0, false
	}
	return label, resourceConfigScore(chunk, preferredLocale), true
}

func resourceConfigScore(typeChunk []byte, preferredLocale string) int {
	configStart := 20
	if len(typeChunk) < configStart+12 {
		return 0
	}
	configSize := int(binary.LittleEndian.Uint32(typeChunk[configStart:]))
	if configSize < 12 || len(typeChunk) < configStart+configSize {
		return 0
	}
	lang := decodeResourceLocale(typeChunk[configStart+8 : configStart+10])
	country := decodeResourceLocale(typeChunk[configStart+10 : configStart+12])
	if lang == "" {
		return 1
	}
	wantLang, wantCountry := splitLocale(preferredLocale)
	if wantLang == "" || lang != wantLang {
		return 0
	}
	if wantCountry != "" && country == wantCountry {
		return 12
	}
	return 10
}

func splitLocale(locale string) (string, string) {
	locale = strings.ToLower(strings.TrimSpace(locale))
	locale = strings.ReplaceAll(locale, "_", "-")
	if locale == "" {
		return "", ""
	}
	parts := strings.Split(locale, "-")
	lang := parts[0]
	country := ""
	if len(parts) > 1 {
		country = parts[1]
	}
	return lang, country
}

func decodeResourceLocale(raw []byte) string {
	if len(raw) < 2 || raw[0] == 0 || raw[1] == 0 {
		return ""
	}
	if raw[0]&0x80 != 0 {
		return ""
	}
	return strings.ToLower(string(raw[:2]))
}

func parseStringPool(chunk []byte) ([]string, error) {
	typ, headerSize, chunkSize, ok := readChunkHeader(chunk, 0)
	if !ok || typ != resStringPoolType || int(chunkSize) > len(chunk) || int(headerSize) < 28 || len(chunk) < 28 {
		return nil, fmt.Errorf("invalid string pool")
	}
	count := int(binary.LittleEndian.Uint32(chunk[8:]))
	flags := binary.LittleEndian.Uint32(chunk[16:])
	stringsStart := int(binary.LittleEndian.Uint32(chunk[20:]))
	if int(headerSize)+count*4 > len(chunk) || stringsStart > len(chunk) {
		return nil, fmt.Errorf("invalid string offsets")
	}
	out := make([]string, count)
	utf8Pool := flags&stringPoolUTF8Flag != 0
	for i := 0; i < count; i++ {
		rel := int(binary.LittleEndian.Uint32(chunk[int(headerSize)+i*4:]))
		start := stringsStart + rel
		if start >= len(chunk) {
			continue
		}
		if utf8Pool {
			out[i] = decodeUTF8String(chunk[start:])
		} else {
			out[i] = decodeUTF16String(chunk[start:])
		}
	}
	return out, nil
}

func decodeUTF8String(data []byte) string {
	_, n := readEncodedLength8(data)
	if n <= 0 || n >= len(data) {
		return ""
	}
	byteLen, m := readEncodedLength8(data[n:])
	start := n + m
	if m <= 0 || byteLen < 0 || start+byteLen > len(data) {
		return ""
	}
	value := data[start : start+byteLen]
	if !utf8.Valid(value) {
		return ""
	}
	return string(value)
}

func decodeUTF16String(data []byte) string {
	unitCount, n := readEncodedLength16(data)
	start := n
	end := start + unitCount*2
	if n <= 0 || unitCount < 0 || end > len(data) {
		return ""
	}
	units := make([]uint16, unitCount)
	for i := 0; i < unitCount; i++ {
		units[i] = binary.LittleEndian.Uint16(data[start+i*2:])
	}
	return string(utf16.Decode(units))
}

func readEncodedLength8(data []byte) (int, int) {
	if len(data) == 0 {
		return 0, 0
	}
	first := int(data[0])
	if first&0x80 == 0 {
		return first, 1
	}
	if len(data) < 2 {
		return 0, 0
	}
	return ((first & 0x7f) << 8) | int(data[1]), 2
}

func readEncodedLength16(data []byte) (int, int) {
	if len(data) < 2 {
		return 0, 0
	}
	first := int(binary.LittleEndian.Uint16(data))
	if first&0x8000 == 0 {
		return first, 2
	}
	if len(data) < 4 {
		return 0, 0
	}
	second := int(binary.LittleEndian.Uint16(data[2:]))
	return ((first & 0x7fff) << 16) | second, 4
}

func readChunkHeader(data []byte, off int) (typ uint16, headerSize uint16, size uint32, ok bool) {
	if off < 0 || off+8 > len(data) {
		return 0, 0, 0, false
	}
	typ = binary.LittleEndian.Uint16(data[off:])
	headerSize = binary.LittleEndian.Uint16(data[off+2:])
	size = binary.LittleEndian.Uint32(data[off+4:])
	if headerSize < 8 || size < uint32(headerSize) || off+int(size) > len(data) {
		return 0, 0, 0, false
	}
	return typ, headerSize, size, true
}

func stringAt(values []string, index uint32) string {
	if index == noIndex || int(index) >= len(values) {
		return ""
	}
	return values[index]
}
